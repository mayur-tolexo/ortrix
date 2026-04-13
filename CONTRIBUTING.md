# Contributing to Ortrix

Thank you for your interest in contributing to Ortrix! This guide will help you get started.

## Project Overview

Ortrix is a Kubernetes-native distributed workflow orchestrator. It uses partitioned execution, push-based task dispatch via gRPC streaming, and an event-sourced WAL for durability. See the [architecture docs](docs/architecture.md) for a deep dive.

### Repository Structure

```
ortrix/
├── api/proto/           # gRPC/Protobuf service definitions
├── cmd/
│   ├── gateway/         # Gateway service (control plane)
│   └── orchestrator/    # Orchestrator service (data plane)
├── internal/
│   ├── config/          # Configuration loading
│   ├── logging/         # Structured logging
│   ├── partition/       # Partition management
│   ├── routing/         # Task routing
│   ├── scheduler/       # Priority scheduling
│   └── wal/             # Write-ahead log
├── pkg/sdk/             # Worker SDK (public API)
├── deploy/
│   ├── helm/            # Helm charts
│   └── k8s/             # Kubernetes manifests
└── docs/                # Design documentation
```

## Running Locally

### Prerequisites

- Go 1.24+
- protoc (Protocol Buffers compiler)
- protoc-gen-go and protoc-gen-go-grpc
- Docker (optional, for container builds)
- kind (optional, for local Kubernetes)

### Build

```bash
# Build all binaries
make build

# Build individual components
make build-gateway
make build-orchestrator
```

### Run

```bash
# Run gateway (default port: 8080)
make run-gateway

# Run orchestrator (default port: 9090)
make run-orchestrator
```

### Test

```bash
# Run all tests with race detection
make test

# Run tests for a specific package
go test ./internal/config/... -v -race -count=1
```

### Lint

```bash
# Requires golangci-lint
make lint
```

### Generate Protobuf

```bash
make proto
```

### Local Kubernetes

```bash
# Create a local cluster
make kind-create

# Build and load images
make docker-all

# Delete cluster
make kind-delete
```

### Environment Variables

| Variable            | Default        | Description            |
|--------------------|----------------|------------------------|
| `GATEWAY_PORT`     | `8080`         | Gateway listen port    |
| `ORCHESTRATOR_PORT`| `9090`         | Orchestrator listen port|
| `ORCHESTRATOR_ADDR`| `localhost:9090`| Orchestrator address   |
| `LOG_LEVEL`        | `debug`        | Log level              |
| `ENVIRONMENT`      | `development`  | Runtime environment    |

## How to Add a New Capability

A "capability" is a task type that a worker can handle. To add support for a new capability:

### 1. Define the Handler

Create a handler function in your service:

```go
func handleMyTask(ctx context.Context, taskID string, payload []byte) ([]byte, error) {
    // Deserialize payload
    var input MyInput
    if err := json.Unmarshal(payload, &input); err != nil {
        return nil, fmt.Errorf("invalid payload: %w", err)
    }

    // Execute task logic
    result, err := doWork(ctx, input)
    if err != nil {
        return nil, err
    }

    // Serialize and return result
    return json.Marshal(result)
}
```

### 2. Register with the Worker SDK

```go
w := sdk.NewWorker("my-service")
w.RegisterHandler("my_task_type", handleMyTask)
w.Start(ctx, orchestratorAddr)
```

### 3. Submit Tasks

Tasks with `type: "my_task_type"` will be automatically routed to workers that registered this capability.

### 4. Add Tests

```go
func TestHandleMyTask(t *testing.T) {
    payload, _ := json.Marshal(MyInput{Field: "value"})
    result, err := handleMyTask(context.Background(), "test-id", payload)
    require.NoError(t, err)
    // Assert on result
}
```

## Coding Standards

### Go Conventions

- Follow [Effective Go](https://go.dev/doc/effective_go) and the [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments)
- Use `gofmt` for formatting (enforced by CI)
- Use `golangci-lint` for static analysis
- Export only what needs to be public
- Keep functions short and focused

### Naming

- Use descriptive names: `partitionOwner` not `po`
- Interfaces: use `-er` suffix where appropriate (`Logger`, `Router`, `Scheduler`)
- Unexported helpers: use clear, lowercase names
- Test files: `*_test.go` alongside the code they test

### Error Handling

- Always handle errors explicitly
- Wrap errors with context: `fmt.Errorf("failed to acquire lease: %w", err)`
- Use sentinel errors for expected conditions
- Don't panic in library code

### Package Structure

- `cmd/` — Entry points only; minimal logic
- `internal/` — Private packages, not importable externally
- `pkg/` — Public SDK, stable API surface

### Comments

- Document all exported types and functions
- Use complete sentences
- Focus on *why*, not *what*

## Testing Expectations

### Requirements

- All new code must have tests
- Maintain or improve test coverage
- Tests must pass with `-race` flag

### Test Types

| Type          | Location                | Command                    |
|--------------|-------------------------|----------------------------|
| Unit tests   | `*_test.go` next to code| `go test ./...`            |
| Integration  | `*_integration_test.go` | `go test -tags=integration`|
| Benchmarks   | `*_test.go`             | `go test -bench=.`        |

### Test Style

```go
func TestPartitionAssignment(t *testing.T) {
    t.Run("assigns to correct partition", func(t *testing.T) {
        mgr := partition.NewDefaultManager()
        p := mgr.AssignPartition("task-123")
        // Assert expected partition
    })

    t.Run("handles empty task ID", func(t *testing.T) {
        // ...
    })
}
```

- Use table-driven tests for multiple cases
- Use `t.Run` for subtests
- Name tests descriptively
- Test edge cases and error paths

## PR Guidelines

### Before Submitting

1. Run `make test` — all tests pass
2. Run `make lint` — no lint errors
3. Run `make build` — builds cleanly
4. Rebase on latest `main`

### PR Description

- Clearly describe **what** the PR does and **why**
- Reference any related issues
- Include before/after if changing behavior
- Add screenshots for UI changes (if applicable)

### PR Size

- Keep PRs small and focused (< 400 lines changed ideally)
- Split large changes into a series of smaller PRs
- One logical change per PR

### Review Process

1. Open a PR against `main`
2. CI must pass (tests, lint, build)
3. At least one maintainer approval required
4. Squash and merge preferred

## Commit Message Conventions

Follow [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <description>

[optional body]

[optional footer]
```

### Types

| Type       | Use Case                                  |
|------------|-------------------------------------------|
| `feat`     | New feature                               |
| `fix`      | Bug fix                                   |
| `docs`     | Documentation only                        |
| `refactor` | Code change that neither fixes nor adds   |
| `test`     | Adding or updating tests                  |
| `chore`    | Build process, CI, dependencies           |
| `perf`     | Performance improvement                   |

### Examples

```
feat(scheduler): add weighted fair queuing for priority levels
fix(wal): handle fsync failure during batch write
docs(architecture): add partition model diagram
test(sdk): add worker registration edge case tests
refactor(routing): extract capability index to separate type
```

### Rules

- Use present tense: "add feature" not "added feature"
- Use lowercase: "fix bug" not "Fix bug"
- Keep the subject line under 72 characters
- Reference issue numbers in the footer: `Closes #42`

## Getting Help

- Open an issue for bugs or feature requests
- Start a discussion for questions or design proposals
- Tag `@maintainers` for urgent items

Thank you for contributing to Ortrix!
