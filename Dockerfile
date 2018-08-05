FROM golang:1.10-alpine3.7 AS builder
ADD . /go/src/github.com/yuichiro-h/cwl-alert-notifier
WORKDIR /go/src/github.com/yuichiro-h/cwl-alert-notifier
RUN go build -ldflags "-s -w" -o bin/cwl-alert-notifier -v

FROM alpine

COPY --from=builder \
    /go/src/github.com/yuichiro-h/cwl-alert-notifier/bin/cwl-alert-notifier \
    /cwl-alert-notifier

ENTRYPOINT ["/cwl-alert-notifier"]