.PHONY: dev build run test lint tidy

dev:
	go run ./cmd/server

build:
	go build -o bin/server ./cmd/server

run: build
	./bin/server

test:
	go test ./...

lint:
	golangci-lint run ./...

tidy:
	go mod tidy

migrate:
	go run ./cmd/server --migrate-only
