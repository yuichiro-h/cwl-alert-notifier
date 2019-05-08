package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/cloudwatchlogs"
	"github.com/gobwas/glob"
	"github.com/yuichiro-h/cwl-alert-notifier/config"
	"github.com/yuichiro-h/cwl-alert-notifier/log"
	"github.com/yuichiro-h/go/aws/sqsrouter"
	"go.uber.org/zap"
)

type AlarmHandler struct {
	alarm config.Alarm
}

func NewAlarmHandler(alarm config.Alarm) func(ctx *sqsrouter.Context) {
	return (&AlarmHandler{
		alarm: alarm,
	}).Handle
}

func (h *AlarmHandler) Handle(ctx *sqsrouter.Context) {
	msg, err := ctx.GetSNSMessage()
	if err != nil {
		log.Get().Error(err.Error())
		ctx.SetDeleteOnFinish(true)
		return
	}

	var cwAlarm CloudWatchAlarm
	if err := json.Unmarshal([]byte(msg.Message), &cwAlarm); err != nil {
		log.Get().Error(err.Error())
		return
	}

	// ログの検索フィルターを取得
	sess := session.Must(session.NewSession())
	cwl := cloudwatchlogs.New(sess)
	descMetricFiltersOut, err := cwl.DescribeMetricFilters(&cloudwatchlogs.DescribeMetricFiltersInput{
		MetricNamespace: aws.String(cwAlarm.Trigger.Namespace),
		MetricName:      aws.String(cwAlarm.Trigger.MetricName),
	})
	if err != nil {
		log.Get().Error(err.Error())
		return
	}
	filter := descMetricFiltersOut.MetricFilters[0]

	log.Get().Info("get metric filter",
		zap.String("metric_namespace", cwAlarm.Trigger.Namespace),
		zap.String("metric_name", cwAlarm.Trigger.MetricName),
		zap.String("log_group", *filter.LogGroupName),
		zap.String("filter", *filter.FilterPattern))

	// ログの取得範囲の算出
	stateChangeTime, err := time.Parse("2006-01-02T15:04:05.999-0700", cwAlarm.StateChangeTime)
	if err != nil {
		log.Get().Error(err.Error())
		return
	}

	logRangeDurationBefore := -3 * time.Minute
	if config.Get().Log.RangeDuration.Before != nil {
		logRangeDurationBefore = time.Duration(-*config.Get().Log.RangeDuration.Before) * time.Second
	}
	logRangeDurationAfter := 3 * time.Minute
	if config.Get().Log.RangeDuration.After != nil {
		logRangeDurationAfter = time.Duration(*config.Get().Log.RangeDuration.After) * time.Second
	}

	startTime := stateChangeTime.Add(logRangeDurationBefore)
	endTime := stateChangeTime.Add(logRangeDurationAfter)

	// ログを取得
	limit := int64(10)
	if config.Get().Log.Limit != nil {
		limit = *config.Get().Log.Limit
	}
	var events []*cloudwatchlogs.FilteredLogEvent
	var nextToken *string
	for {
		out, err := cwl.FilterLogEvents(&cloudwatchlogs.FilterLogEventsInput{
			LogGroupName:  filter.LogGroupName,
			FilterPattern: filter.FilterPattern,
			Limit:         aws.Int64(limit),
			StartTime:     aws.Int64(startTime.Unix() * 1000),
			EndTime:       aws.Int64(endTime.Unix() * 1000),
			NextToken:     nextToken,
		})
		if err != nil {
			if awsErr, ok := err.(awserr.Error); ok {
				// 1秒(TPS)/アカウント/リージョンあたり5件のトランザクションのためAPI Limitになりやすい
				// API Limitの時は1秒スリープしてからリトライする
				if awsErr.Code() == "ThrottlingException" {
					time.Sleep(1 * time.Second)
					continue
				} else {
					log.Get().Error(err.Error())
					return
				}
			}
			log.Get().Error(err.Error())
			return
		}

		if len(out.Events) > 0 {
			events = append(events, out.Events...)
		}

		nextToken = out.NextToken

		if nextToken == nil {
			break
		}
	}

	log.Get().Info("get log event",
		zap.Int64("limit", limit),
		zap.Time("start_time", startTime),
		zap.Time("end_time", endTime),
		zap.Int("count", len(events)))

	// ログを通知
	var notifyInputs []notifyInput
	for _, e := range events {
		var appName string

		// Slackへの通知設定を取得
		slack := config.Get().Slack

		if *filter.LogGroupName == "/aws/batch/job" {
			// AWS Batchのログストリーム名は{jobDefinitionName}/default/{ecs_task_id}の形式
			jobDefinitionName := strings.Split(*e.LogStreamName, "/")[0]
			appName = fmt.Sprintf("%s(AWS Batch)", jobDefinitionName)

		L1:
			for _, g := range h.alarm.Groups {
				for _, def := range g.AWSBatchJobDefinitions {
					if glob.MustCompile(def).Match(jobDefinitionName) {
						slack.Merge(h.alarm.Slack)
						slack.Merge(g.Slack)
						break L1
					}
				}
			}
		} else {
			appName = *filter.LogGroupName

		L2:
			for _, g := range h.alarm.Groups {
				for _, lg := range g.LogGroups {
					if glob.MustCompile(lg).Match(appName) {
						slack.Merge(h.alarm.Slack)
						slack.Merge(g.Slack)
						break L2
					}
				}
			}
		}

		// ログイベント発生日時
		eventAt := time.Unix(*e.Timestamp/1000, 0).In(time.Local)

		// ログ内容
		var body string
		var msg map[string]interface{}
		if err := json.Unmarshal([]byte(*e.Message), &msg); err != nil {
			body = *e.Message
		} else {
			data, err := json.MarshalIndent(&msg, "", "    ")
			if err != nil {
				log.Get().Error(err.Error())
				return
			}
			body = string(data)
		}

		log.Get().Debug("get log event",
			zap.String("app_name", appName),
			zap.String("log_stream_name", *e.LogStreamName),
			zap.Any("slack", slack),
			zap.String("msg", *e.Message),
			zap.Time("event_at", eventAt))

		var exists bool
		for i, n := range notifyInputs {
			// 一度の通知で同一のジョブ定義のエラーがある場合は
			// 先頭のログ移行はログ内容のみを通知する
			if n.ApplicationName == appName {
				notifyInputs[i].Body = append(notifyInputs[i].Body, string(body))
				exists = true
				break
			}
		}
		if exists {
			continue
		}

		// CloudWatchコンソールのURLを組み立て
		urlBuilder := strings.Builder{}
		urlBuilder.WriteString(fmt.Sprintf("https://%s.console.aws.amazon.com/cloudwatch/home?", config.Get().AWS.Region))
		urlBuilder.WriteString(fmt.Sprintf("region=%s", config.Get().AWS.Region))
		urlBuilder.WriteString(fmt.Sprintf("#logEventViewer:group=%s;", *filter.LogGroupName))
		urlBuilder.WriteString(fmt.Sprintf("stream=%s;", *e.LogStreamName))
		urlBuilder.WriteString(fmt.Sprintf("start=%s", eventAt.UTC().Format(time.RFC3339)))

		notifyInputs = append(notifyInputs, notifyInput{
			ApplicationName: appName,
			Slack:           slack,
			FirstLogURL:     urlBuilder.String(),
			Body:            []string{string(body)},
		})
	}

	for _, n := range notifyInputs {
		if err := notify(&n); err != nil {
			log.Get().Error(err.Error())
			return
		}
	}

	ctx.SetDeleteOnFinish(true)
}
