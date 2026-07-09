BINARY  := hall-clock
PKG     := ./src/hall-clock
WEB_DIR := src/hall-clock/web

.DEFAULT_GOAL := help

.PHONY: help build run test race vet fmt tidy build-pi clean

help: ## List targets
	@grep -hE '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'

build: ## Build the binary
	go build -o $(BINARY) $(PKG)

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
	GOOS=linux GOARCH=arm64 go build -o dist/hall-clock-arm64 $(PKG)

clean: ## Remove build artifacts
	rm -f $(BINARY)
	rm -rf dist
