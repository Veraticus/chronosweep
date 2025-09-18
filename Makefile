.PHONY: build test lint

build:
	go build ./cmd/...

test:
	gotestsum --format dots -- -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

lint:
	gofmt -w .
	golangci-lint run
	deadcode -test ./...

