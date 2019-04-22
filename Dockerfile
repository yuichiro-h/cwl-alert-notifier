FROM golang:1.12 as build

ADD . /go/src/github.com/yuichiro-h/cwl-alert-notifier
WORKDIR /go/src/github.com/yuichiro-h/cwl-alert-notifier
RUN go build -ldflags "-s -w" -o /go/bin/exec

FROM gcr.io/distroless/base
COPY --from=build /go/bin/exec /
CMD ["/exec"]