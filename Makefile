BINARY  := rudeproxy
VERSION ?= $(or $(shell git describe --tags --always --dirty),dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all build run test cover lint fmt vet vuln tidy clean

all: fmt vet lint test build

build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY)$(if $(filter Windows_NT,$(OS)),.exe,) ./cmd/rudeproxy

run:
	go run ./cmd/rudeproxy $(ARGS)

test:
	go test -race ./...

cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

lint:
	go tool golangci-lint run

fmt:
	go tool golangci-lint fmt

vet:
	go vet ./...

vuln:
	go tool govulncheck ./...

tidy:
	go mod tidy

clean:
	go clean
	rm -rf bin coverage.out
