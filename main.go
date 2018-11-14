package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
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

func main() {
	if err := config.Load(os.Getenv("CONFIG_PATH")); err != nil {
		panic(err)
		return
	}
	log.SetConfig(log.Config{
		Debug: config.Get().Debug,
	})

	region := aws.NewConfig().WithRegion(config.Get().AWS.Region)
	sess, err := session.NewSession(region)
	if err != nil {
		log.Get().Error(err.Error())
		return
	}
	r := sqsrouter.New(sess, sqsrouter.WithLogger(log.Get()))
	r.AddHandler(config.Get().AWS.AlarmSqsURL, execute)
	r.Start()

	ch := make(chan os.Signal)
	signal.Notify(ch, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-ch

	r.Stop()
}

func execute(ctx *sqsrouter.Context) {
	region := aws.NewConfig().WithRegion(config.Get().AWS.Region)
	sess, err := session.NewSession(region)
	if err != nil {
		log.Get().Error(err.Error())
		return
	}
	msg, err := ctx.GetSNSMessage()
	if err != nil {
		log.Get().Error(err.Error())
		return
	}

	var alarm Alarm
	if err := json.Unmarshal([]byte(msg.Message), &alarm); err != nil {
		log.Get().Error(err.Error())
		return
	}

	var notifyInputs []notifyInput

	cwl := cloudwatchlogs.New(sess)
	descMetricFiltersOut, err := cwl.DescribeMetricFilters(&cloudwatchlogs.DescribeMetricFiltersInput{
		MetricNamespace: aws.String(alarm.Trigger.Namespace),
		MetricName:      aws.String(alarm.Trigger.MetricName),
	})
	if err != nil {
		log.Get().Error(err.Error())
		return
	}
	filter := descMetricFiltersOut.MetricFilters[0]

	log.Get().Info("get metric filter",
		zap.String("metric_namespace", alarm.Trigger.Namespace),
		zap.String("metric_name", alarm.Trigger.MetricName),
		zap.String("log_group", *filter.LogGroupName),
		zap.String("filter", *filter.FilterPattern))

	stateChangeTime, err := time.Parse("2006-01-02T15:04:05.999-0700", alarm.StateChangeTime)
	if err != nil {
		log.Get().Error(err.Error())
		return
	}

	startTime := stateChangeTime.Add(time.Second * time.Duration(-config.Get().Log.RangeDuration.Before))
	endTime := stateChangeTime.Add(time.Second * time.Duration(config.Get().Log.RangeDuration.After))

	var events []*cloudwatchlogs.FilteredLogEvent
	var nextToken *string
	for {
		out, err := cwl.FilterLogEvents(&cloudwatchlogs.FilterLogEventsInput{
			LogGroupName:  filter.LogGroupName,
			FilterPattern: filter.FilterPattern,
			Limit:         aws.Int64(config.Get().Log.Limit),
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
		zap.Int64("limit", config.Get().Log.Limit),
		zap.Time("start_time", startTime),
		zap.Time("end_time", endTime),
		zap.Int("count", len(events)))

	for _, e := range events {
		var appName string

		// Slackの通知先を取得
		channel := config.Get().Slack.DefaultChannel
		if *filter.LogGroupName == "/aws/batch/job" {
			// AWS Batchのログストリーム名は{jobDefinitionName}/default/{ecs_task_id}の形式
			jobDefinitionName := strings.Split(*e.LogStreamName, "/")[0]
			appName = fmt.Sprintf("%s(AWS Batch)", jobDefinitionName)

			for _, a := range config.Get().AWS.AWSBatch {
				if a.SlackChannel != nil && glob.MustCompile(a.JobDefinitionName).Match(jobDefinitionName) {
					channel = *a.SlackChannel
					break
				}
			}
		} else {
			appName = *filter.LogGroupName

			for _, a := range config.Get().AWS.LogGroup {
				if a.SlackChannel != nil && glob.MustCompile(a.Name).Match(appName) {
					channel = *a.SlackChannel
					break
				}
			}
		}
		if channel == "" {
			channel = config.Get().Slack.DefaultChannel
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
			zap.String("channel", channel),
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
			Channel:         channel,
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
