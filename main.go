package main

import (
	"os"
	"os/signal"

	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/yuichiro-h/cwl-alert-notifier/config"
	"github.com/yuichiro-h/cwl-alert-notifier/log"
	"github.com/yuichiro-h/go/aws/sqsrouter"
)

func main() {
	if err := config.Load(os.Getenv("CONFIG_PATH")); err != nil {
		panic(err)
	}
	log.SetConfig(log.Config{
		Debug: config.Get().Debug,
	})

	sess := session.Must(session.NewSession())
	r := sqsrouter.New(sess, sqsrouter.WithLogger(log.Get()))

	for _, alarm := range config.Get().Alarms {
		r.AddHandler(alarm.SqsURL, NewAlarmHandler(alarm))
	}

	r.Start()

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	<-ch

	r.Stop()
}
