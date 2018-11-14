package sqsrouter

import (
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/sqs"
	"go.uber.org/zap"
)

type SQSRouter struct {
	handlers  []handler
	termChs   []chan chan int
	logger    *zap.Logger
	sqsClient *sqs.SQS
}

type Option func(r *SQSRouter)

func WithLogger(l *zap.Logger) Option {
	return func(r *SQSRouter) {
		r.logger = l
	}
}

type handlerFunc func(*Context)

type handler struct {
	function handlerFunc
	queueURL string
	async    bool
}

func New(sess *session.Session, options ...Option) *SQSRouter {
	r := SQSRouter{
		logger:    zap.NewNop(),
		sqsClient: sqs.New(sess),
	}
	for _, o := range options {
		o(&r)
	}

	return &r
}

func (s *SQSRouter) addHandler(queueURL string, async bool, h handlerFunc) {
	s.handlers = append(s.handlers, handler{
		function: h,
		queueURL: queueURL,
		async:    async,
	})
}

func (s *SQSRouter) AddHandler(queueURL string, h handlerFunc) {
	s.addHandler(queueURL, false, h)
}

func (s *SQSRouter) AddAsyncHandler(queueURL string, h handlerFunc) {
	s.addHandler(queueURL, true, h)
}

func (s *SQSRouter) Start() {
	var termChs []chan chan int
	for i := 0; i < len(s.handlers); i++ {
		termChs = append(termChs, make(chan chan int))
	}
	s.termChs = termChs

	for i := range s.handlers {
		go func(i int) {
			s.listen(&s.handlers[i], termChs[i])
		}(i)
	}
}

func (s *SQSRouter) Stop() {
	for i := 0; i < len(s.handlers); i++ {
		exitCh := make(chan int)
		s.termChs[i] <- exitCh
		<-exitCh
	}
}

func (s *SQSRouter) listen(h *handler, termCh chan chan int) {
	for {
		select {
		case exitCh := <-termCh:
			exitCh <- 0
			return
		default:
			res, err := s.sqsClient.ReceiveMessage(&sqs.ReceiveMessageInput{
				MaxNumberOfMessages: aws.Int64(1),
				QueueUrl:            aws.String(h.queueURL),
			})
			if err != nil {
				if awsErr, ok := err.(awserr.Error); ok {
					if awsErr == credentials.ErrNoValidProvidersFoundInChain {
						s.logger.Warn(awsErr.Message())
						continue
					}
					if awsErr.Code() == sqs.ErrCodeQueueDoesNotExist {
						panic(fmt.Sprintf("Not found queue. url=%s", h.queueURL))
					}
					s.logger.Error(awsErr.Message(), zap.Any("error", awsErr))
					continue
				} else {
					s.logger.Error(err.Error())
				}
			}

			for _, msg := range res.Messages {
				if h.async {
					go func(msg *sqs.Message) {
						s.invokeHandler(h, msg)
					}(msg)
				} else {
					s.invokeHandler(h, msg)
				}
			}
		}
	}
}

func (s *SQSRouter) invokeHandler(h *handler, msg *sqs.Message) {
	c := Context{
		Message:       msg,
		deleteMessage: false,
	}

	h.function(&c)

	if c.deleteMessage {
		_, err := s.sqsClient.DeleteMessage(&sqs.DeleteMessageInput{
			QueueUrl:      aws.String(h.queueURL),
			ReceiptHandle: msg.ReceiptHandle,
		})
		if err != nil {
			s.logger.Error(err.Error())
		}
	}
}
