package sqsrouter

import (
	"encoding/json"

	"github.com/aws/aws-sdk-go/service/sqs"
	"github.com/pkg/errors"
)

type Context struct {
	Message       *sqs.Message
	deleteMessage bool
}

type SNSMessage struct {
	Type             string `json:"Type"`
	MessageID        string `json:"MessageId"`
	TopicArn         string `json:"TopicArn"`
	Message          string `json:"Message"`
	Timestamp        string `json:"Timestamp"`
	SignatureVersion string `json:"SignatureVersion"`
	Signature        string `json:"Signature"`
	SignatureCertURL string `json:"SignatureCertURL"`
	UnsubscribeURL   string `json:"UnsubscribeURL"`
}

func (c *Context) GetSNSMessage() (*SNSMessage, error) {
	var msg SNSMessage
	err := json.Unmarshal([]byte(*c.Message.Body), &msg)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	return &msg, nil
}

func (c *Context) SetDeleteOnFinish(delete bool) {
	c.deleteMessage = delete
}
