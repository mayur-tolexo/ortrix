.PHONY: all build build-gateway build-orchestrator test lint run-gateway run-orchestrator \
       proto docker-gateway docker-orchestrator docker-all kind-create kind-delete clean

# Binary output directory
BIN_DIR := bin

# Go settings
GO := go
GOFLAGS := -ldflags="-s -w"

# Docker settings
DOCKER_REPO := flowd
DOCKER_TAG := latest

# ──────────────────────────────────────────────
# Build
# ──────────────────────────────────────────────

all: build

build: build-gateway build-orchestrator

build-gateway:
	$(GO) build $(GOFLAGS) -o $(BIN_DIR)/gateway ./cmd/gateway

build-orchestrator:
	$(GO) build $(GOFLAGS) -o $(BIN_DIR)/orchestrator ./cmd/orchestrator

# ──────────────────────────────────────────────
# Test & Lint
# ──────────────────────────────────────────────

test:
	$(GO) test ./... -v -race -count=1

lint:
	golangci-lint run ./...

# ──────────────────────────────────────────────
# Run locally
# ──────────────────────────────────────────────

run-gateway: build-gateway
	./$(BIN_DIR)/gateway

run-orchestrator: build-orchestrator
	./$(BIN_DIR)/orchestrator

# ──────────────────────────────────────────────
# Protobuf generation
# ──────────────────────────────────────────────

proto:
	protoc \
		--go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		api/proto/flowd.proto

# ──────────────────────────────────────────────
# Docker
# ──────────────────────────────────────────────

docker-gateway:
	docker build -f cmd/gateway/Dockerfile -t $(DOCKER_REPO)/gateway:$(DOCKER_TAG) .

docker-orchestrator:
	docker build -f cmd/orchestrator/Dockerfile -t $(DOCKER_REPO)/orchestrator:$(DOCKER_TAG) .

docker-all: docker-gateway docker-orchestrator

# ──────────────────────────────────────────────
# Kind cluster (local Kubernetes)
# ──────────────────────────────────────────────

kind-create:
	kind create cluster --name flowd

kind-delete:
	kind delete cluster --name flowd

# ──────────────────────────────────────────────
# Clean
# ──────────────────────────────────────────────

clean:
	rm -rf $(BIN_DIR)
