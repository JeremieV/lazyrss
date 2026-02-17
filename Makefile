BINARY_NAME=lazyrss

.PHONY: build install clean test dist dist-snapshot help

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-15s\033[0m %s\n", $$1, $$2}'

build: ## Build for current OS and arch
	go build -o $(BINARY_NAME) main.go

install: ## Install binary to $GOPATH/bin
	go install .

clean: ## Clean build artifacts
	go clean
	rm -f $(BINARY_NAME)
	rm -rf dist/

test: ## Run tests
	go test ./...

dist: ## Build for all platforms using GoReleaser (requires GoReleaser)
	goreleaser release --clean

dist-snapshot: ## Build for all platforms locally (snapshot)
	goreleaser release --snapshot --clean --skip=publish

