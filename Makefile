# Mortise — top-level Makefile.
#
# Wraps npm and Go commands so the proto + lint + test pipeline has a single
# entry point. See README.md for the full workflow.

SHELL := /bin/bash
export PATH := $(HOME)/go/bin:$(PATH)

.PHONY: help
help: ## Show this help.
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z0-9_.-]+:.*?## / {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: bootstrap
bootstrap: ## Install toolchain (buf, protoc plugins, golangci-lint, node deps).
	@echo "==> Go: protoc-gen-go, protoc-gen-connect-go, golangci-lint"
	@command -v buf         >/dev/null || GOBIN=$$(go env GOPATH)/bin go install github.com/bufbuild/buf/cmd/buf@v1.72.0
	@command -v protoc-gen-go >/dev/null || GOBIN=$$(go env GOPATH)/bin go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
	@command -v protoc-gen-connect-go >/dev/null || GOBIN=$$(go env GOPATH)/bin go install connectrpc.com/connect/cmd/protoc-gen-connect-go@v1.20.0
	@command -v golangci-lint >/dev/null || GOBIN=$$(go env GOPATH)/bin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2
	@command -v protoc       >/dev/null || (echo "protoc missing — install with: brew install protobuf" && exit 1)
	@echo "==> Node deps via npm"
	@npm install

.PHONY: proto
proto: ## Regenerate Go + TypeScript stubs from proto/*.proto.
	@buf generate

.PHONY: proto-check
proto-check: ## Verify generated stubs are up-to-date (CI gate).
	@buf generate
	@git diff --exit-code daemon/gen tui/src/gen

.PHONY: build
build: ## Compile the mortised Go binary into ./bin/mortised.
	@mkdir -p bin
	@cd daemon && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o ../bin/mortised ./cmd/mortised

.PHONY: lint
lint: ## Run all linters (TS + Go).
	@npm run lint

.PHONY: lint-go
lint-go: ## Run Go linters (gofmt, go vet, golangci-lint).
	@npm run lint:go

.PHONY: lint-ts
lint-ts: ## Run TS linters (prettier, eslint, tsc).
	@npm run lint:ts

.PHONY: format
format: ## Auto-format all source files.
	@npm run format:fix
	@npm run format:go

.PHONY: format-check
format-check: ## Verify formatting only (no edits).
	@npm run format:check

.PHONY: test
test: ## Run all test suites.
	@cd daemon && go test ./... -count=1
	@npm test

.PHONY: ci
ci: proto-check format-check lint test build ## Full CI pipeline (used by GitHub Actions).

.PHONY: clean
clean: ## Remove generated proto stubs and build artifacts.
	@rm -rf daemon/gen tui/src/gen bin
