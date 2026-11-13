BINARY    := rpu
BUILD_DIR := bin
CMD_DIR   := ./cmd/rpu

.DEFAULT_GOAL := help

.PHONY: build
build: ## Build the rpu binary
	go build -o $(BUILD_DIR)/$(BINARY) $(CMD_DIR)

.PHONY: build-static-linux-amd64
build-static-linux-amd64: ## Build a static linux/amd64 binary
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(BUILD_DIR)/$(BINARY) $(CMD_DIR)

.PHONY: test-unit
test-unit: ## Run unit tests
	go test ./...

.PHONY: test-integration
test-integration: ## Run integration tests
	cd test/integration && go test -tags=integration -v ./...

.PHONY: test
test: test-unit ## Run unit tests (alias for test-unit)

.PHONY: lint
lint: ## Run golangci-lint and auto-fix issues
	golangci-lint run --fix ./...
	cd test/integration && golangci-lint run --fix ./...

.PHONY: tidy
tidy: ## Run go mod tidy for all modules
	go mod tidy
	cd test/integration && go mod tidy

.PHONY: clean
clean: ## Remove the built binary and test cache
	rm -f $(BUILD_DIR)/$(BINARY)
	go clean -testcache

.PHONY: help
help: ## Show this help message
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-28s\033[0m %s\n", $$1, $$2}'
