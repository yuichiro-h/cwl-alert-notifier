package main

import (
	"fmt"
	"strings"

	"github.com/nlopes/slack"
	"github.com/pkg/errors"
	"github.com/yuichiro-h/cwl-alert-notifier/config"
)

type notifyInput struct {
	ApplicationName string
	Channel         string
	FirstLogURL     string
	Body            []string
}

func notify(in *notifyInput) error {
	body := strings.Builder{}
	for _, b := range in.Body {
		body.WriteString("```")
		body.WriteString(b)
		body.WriteString("```")
		body.WriteString("\n")
	}

	attachment := slack.Attachment{
		Color:      config.Get().Slack.AttachmentColor,
		MarkdownIn: []string{"text"},
		Text:       body.String(),
		Actions: []slack.AttachmentAction{
			{
				Type: "button",
				Text: "Open Head Log",
				URL:  in.FirstLogURL,
			},
		},
	}

	params := slack.PostMessageParameters{
		Markdown:    true,
		Username:    config.Get().Slack.Username,
		IconURL:     config.Get().Slack.IconURL,
		Attachments: []slack.Attachment{attachment},
	}

	text := fmt.Sprintf("Found log in *%s*", in.ApplicationName)
	_, _, err := slack.New(config.Get().Slack.APIToken).PostMessage(in.Channel, text, params)
	if err != nil {
		return errors.WithStack(err)
	}

	return nil
}
