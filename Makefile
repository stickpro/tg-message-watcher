.PHONY:
.SILENT:
.DEFAULT_GOAL := run

build:
	go mod download && go build -o ./.bin/telegram-wathcer ./cmd/app/main.go
run: build
	./.bin/telegram-wathcer