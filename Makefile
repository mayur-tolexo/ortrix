.PHONY: all build build-gateway build-orchestrator test test-coverage test-coverage-report \
       lint run-gateway run-orchestrator proto proto-clean docker-gateway docker-orchestrator \
       docker-all kind-create kind-delete clean swagger help

# Binary output directory
BIN_DIR := bin

# Go settings
GO := go
GOFLAGS := -ldflags="-s -w"

# Docker settings
DOCKER_REPO := ortrix
DOCKER_TAG := latest

# Swagger settings
SWAGGER_DIR := docs/swagger

# ──────────────────────────────────────────────
# Help
# ──────────────────────────────────────────────

.DEFAULT_GOAL := help

help: ## Show available commands
	@echo "Available commands:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
	awk 'BEGIN {FS = ":.*?## "}; {printf "  %-25s %s\n", $$1, $$2}'

# ──────────────────────────────────────────────
# Build
# ──────────────────────────────────────────────

all: build ## Build all services

build: build-gateway build-orchestrator ## Build all services

build-gateway: ## Build the gateway binary
	$(GO) build $(GOFLAGS) -o $(BIN_DIR)/gateway ./cmd/gateway

build-orchestrator: ## Build the orchestrator binary
	$(GO) build $(GOFLAGS) -o $(BIN_DIR)/orchestrator ./cmd/orchestrator

# ──────────────────────────────────────────────
# Test & Lint
# ──────────────────────────────────────────────

test: ## Run tests with race detection
	$(GO) test ./... -v -race -count=1

test-coverage: ## Run tests with coverage report
	$(GO) test ./... -v -race -count=1 -coverprofile=coverage.out
	@echo "✓ Coverage report generated: coverage.out"

test-coverage-report: test-coverage ## Run tests and display coverage report
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "✓ HTML coverage report generated: coverage.html"
	@echo "Opening coverage report in browser..."
	@open coverage.html || xdg-open coverage.html || echo "Please open coverage.html manually"

test-coverage-by-package: test-coverage ## Show coverage by package
	@$(GO) tool cover -func=coverage.out | grep -E "^github.com/mayur-tolexo/ortrix" | sort
	@echo ""
	@$(GO) tool cover -func=coverage.out | tail -1

lint: ## Run linter
	golangci-lint run ./...

# ──────────────────────────────────────────────
# Swagger
# ──────────────────────────────────────────────

swagger: ## Generate Swagger docs into docs/swagger/
	swag init -g cmd/gateway/main.go -o $(SWAGGER_DIR) --exclude api/proto

# ──────────────────────────────────────────────
# Run locally
# ──────────────────────────────────────────────

run-gateway: build-gateway ## Run gateway server (gRPC + HTTP with Swagger)
	./$(BIN_DIR)/gateway

run-orchestrator: build-orchestrator ## Run orchestrator server
	./$(BIN_DIR)/orchestrator

# ──────────────────────────────────────────────
# Protobuf generation
# ──────────────────────────────────────────────

proto: ## Generate protobuf Go code from .proto files
	@echo "Generating protobuf code..."
	protoc \
		--go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		api/proto/ortrix.proto
	@echo "✓ Protobuf files generated successfully"

proto-clean: ## Clean generated protobuf files
	@echo "Cleaning generated protobuf files..."
	rm -f api/proto/*.pb.go
	@echo "✓ Generated protobuf files cleaned"

# ──────────────────────────────────────────────
# Docker
# ──────────────────────────────────────────────

docker-gateway: ## Build Docker image for gateway
	docker build -f cmd/gateway/Dockerfile -t $(DOCKER_REPO)/gateway:$(DOCKER_TAG) .

docker-orchestrator: ## Build Docker image for orchestrator
	docker build -f cmd/orchestrator/Dockerfile -t $(DOCKER_REPO)/orchestrator:$(DOCKER_TAG) .

docker-all: docker-gateway docker-orchestrator ## Build all Docker images

# ──────────────────────────────────────────────
# Kind cluster (local Kubernetes)
# ──────────────────────────────────────────────

kind-create: ## Create a local Kind cluster
	kind create cluster --name ortrix

kind-delete: ## Delete the local Kind cluster
	kind delete cluster --name ortrix

# ──────────────────────────────────────────────
# Clean
# ──────────────────────────────────────────────

clean: ## Remove build artifacts
	rm -rf $(BIN_DIR)
