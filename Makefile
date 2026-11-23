# PATH com o Go do laptop-2023.
export PATH := /usr/local/go/bin:$(PATH)

.PHONY: build test

build:
	go build -o blocos-de-rua-mcp ./cmd/blocos-de-rua-mcp

test:
	go test ./...
