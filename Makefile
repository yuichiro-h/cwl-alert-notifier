BINARY_NAME=cwl-alert-notifier

all: build
run:
	go run *.go --config config.dev.yml
build:
	go build -o bin/$(BINARY_NAME) -v
test:
	go test -v ./...