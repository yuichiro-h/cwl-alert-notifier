BINARY_NAME=cwl-alert-notifier

all: build
run:
	CONFIG_PATH=config.dev.yml go run *.go
build:
	go build -o bin/$(BINARY_NAME) -v
test:
	go test -v ./...