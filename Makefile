BINARY  := hall-clock
PKG     := ./src/hall-clock
WEB_DIR := src/hall-clock/web
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.DEFAULT_GOAL := help

.PHONY: help build run test race vet fmt tidy build-pi build-pi-armv7 build-pi-armv6 clean

help: ## List targets
	@grep -hE '^[a-z0-9-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

build: ## Build the binary
	go build -ldflags '$(LDFLAGS)' -o $(BINARY) $(PKG)

run: ## Run locally with live assets on :8480
	go run $(PKG) -web-dir $(WEB_DIR)

test: ## Run tests
	go test ./...

race: ## Run tests with the race detector
	go test -race ./...

vet: ## Run go vet
	go vet ./...

fmt: ## Format all Go code
	gofmt -w src

tidy: ## Tidy module dependencies
	go mod tidy

build-pi: ## Cross-compile for Raspberry Pi (arm64) into dist/
	GOOS=linux GOARCH=arm64 go build -ldflags '$(LDFLAGS)' -o dist/hall-clock-linux-arm64 $(PKG)

build-pi-armv7: ## Cross-compile for older 32-bit Pi OS (armv7) into dist/
	GOOS=linux GOARCH=arm GOARM=7 go build -ldflags '$(LDFLAGS)' -o dist/hall-clock-linux-armv7 $(PKG)

build-pi-armv6: ## Cross-compile for Pi Zero / Pi 1 (armv6) into dist/
	GOOS=linux GOARCH=arm GOARM=6 go build -ldflags '$(LDFLAGS)' -o dist/hall-clock-linux-armv6 $(PKG)

clean: ## Remove build artifacts
	rm -f $(BINARY)
	rm -rf dist
