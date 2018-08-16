package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go/service/sqs"
	"github.com/gobwas/glob"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
	"github.com/yuichiro-h/cwl-alert-notifier/config"
	"github.com/yuichiro-h/cwl-alert-notifier/log"
	"go.uber.org/zap"
)

func main() {
	app := cli.NewApp()
	app.Name = "cwl-alert-notifier"
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name: "config",
		},
	}
	app.Before = func(ctx *cli.Context) error {
		configFilename := ctx.String("config")
		if err := config.Load(configFilename); err != nil {
			return err
		}

		return nil
	}
	app.Action = func(ctx *cli.Context) error {
		if err := execute(); err != nil {
			log.Get().Error("error occurred", zap.String("cause", fmt.Sprintf("%+v", err)))
		}
		return nil
	}
	app.Run(os.Args)
}

func execute() error {
	region := aws.NewConfig().WithRegion(config.Get().AWS.Region)
	sess, err := session.NewSession(region)
	if err != nil {
		return errors.WithStack(err)
	}
	sqsCli := sqs.New(sess)

	var alarms []Alarm
	var sqsReceiptHandles []*string
	for {
		receiveMessageOut, err := sqsCli.ReceiveMessage(&sqs.ReceiveMessageInput{
			MaxNumberOfMessages: aws.Int64(10),
			QueueUrl:            aws.String(config.Get().AWS.AlarmSqsURL),
		})
		if err != nil {
			return errors.WithStack(err)
		}
		if len(receiveMessageOut.Messages) == 0 {
			log.Get().Debug("not found messages", zap.String("queue_url", config.Get().AWS.AlarmSqsURL))
			break
		}

		for _, msg := range receiveMessageOut.Messages {
			var snsPayload map[string]interface{}
			if err := json.Unmarshal([]byte(*msg.Body), &snsPayload); err != nil {
				return errors.WithStack(err)
			}

			var alarm Alarm
			if err := json.Unmarshal([]byte(snsPayload["Message"].(string)), &alarm); err != nil {
				return errors.WithStack(err)
			}
			alarms = append(alarms, alarm)

			sqsReceiptHandles = append(sqsReceiptHandles, msg.ReceiptHandle)
		}
	}
	log.Get().Info("found alarm", zap.Int("count", len(alarms)))

	var notifyInputs []notifyInput
	cwl := cloudwatchlogs.New(sess)
	for _, a := range alarms {
		descMetricFiltersOut, err := cwl.DescribeMetricFilters(&cloudwatchlogs.DescribeMetricFiltersInput{
			MetricNamespace: aws.String(a.Trigger.Namespace),
			MetricName:      aws.String(a.Trigger.MetricName),
		})
		if err != nil {
			return errors.WithStack(err)
		}
		filter := descMetricFiltersOut.MetricFilters[0]

		log.Get().Info("get metric filter",
			zap.String("metric_namespace", a.Trigger.Namespace),
			zap.String("metric_name", a.Trigger.MetricName),
			zap.String("log_group", *filter.LogGroupName),
			zap.String("filter", *filter.FilterPattern))

		stateChangeTime, err := time.Parse("2006-01-02T15:04:05.999-0700", a.StateChangeTime)
		if err != nil {
			return errors.WithStack(err)
		}
		startTime := stateChangeTime.Add(time.Second * time.Duration(-config.Get().Log.RangeDuration.Before))
		endTime := stateChangeTime.Add(time.Second * time.Duration(config.Get().Log.RangeDuration.After))

		filterLogEventOut, err := cwl.FilterLogEvents(&cloudwatchlogs.FilterLogEventsInput{
			LogGroupName:  filter.LogGroupName,
			FilterPattern: filter.FilterPattern,
			Limit:         aws.Int64(config.Get().Log.Limit),
			StartTime:     aws.Int64(startTime.Unix() * 1000),
			EndTime:       aws.Int64(endTime.Unix() * 1000),
		})
		if err != nil {
			return errors.WithStack(err)
		}
		log.Get().Info("get log event",
			zap.Int64("limit", config.Get().Log.Limit),
			zap.Time("start_time", startTime),
			zap.Time("end_time", endTime),
			zap.Int("count", len(filterLogEventOut.Events)))

		for _, e := range filterLogEventOut.Events {
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
					}
				}
			} else {
				appName = *filter.LogGroupName

				for _, a := range config.Get().AWS.LogGroup {
					if a.SlackChannel != nil && glob.MustCompile(a.Name).Match(appName) {
						channel = *a.SlackChannel
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
					return errors.WithStack(err)
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
	}

	for _, n := range notifyInputs {
		if err := notify(&n); err != nil {
			return errors.WithStack(err)
		}
	}

	for _, h := range sqsReceiptHandles {
		_, err = sqsCli.DeleteMessage(&sqs.DeleteMessageInput{
			QueueUrl:      aws.String(config.Get().AWS.AlarmSqsURL),
			ReceiptHandle: h,
		})
		if err != nil {
			log.Get().Error(err.Error())
			continue
		}
	}

	return nil
}
