.PHONY: all build build-gateway build-orchestrator test lint run-gateway run-orchestrator \
       proto docker-gateway docker-orchestrator docker-all kind-create kind-delete clean \
       swagger help

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

lint: ## Run linter
	golangci-lint run ./...

# ──────────────────────────────────────────────
# Swagger
# ──────────────────────────────────────────────

swagger: ## Generate Swagger docs into docs/swagger/
	swag init -g cmd/gateway/main.go -o $(SWAGGER_DIR)

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

proto: ## Generate protobuf Go code
	protoc \
		--go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		api/proto/ortrix.proto

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
