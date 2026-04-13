# Ortrix Testing Strategy

## Table of Contents

- [Overview](#overview)
- [Unit Testing](#unit-testing)
- [Integration Testing](#integration-testing)
- [End-to-End Testing](#end-to-end-testing)
- [Failure Testing](#failure-testing)
- [Load Testing](#load-testing)
- [CI Requirements](#ci-requirements)

---

## Overview

Testing is a critical part of the Ortrix development process. Every component — from the WAL to the scheduler to the Worker SDK — must be thoroughly tested to ensure correctness, reliability, and performance under failure conditions. This document defines the testing strategy, coverage requirements, and CI enforcement policies.

---

## Unit Testing

### Coverage Target

- **Minimum 80% code coverage** across all packages
- Coverage is measured per-package and enforced in CI
- New code must include tests — PRs without tests for new functionality will not be merged

### Approach: Table-Driven Tests (Go)

All unit tests follow Go's table-driven test pattern for clarity and maintainability:

```go
func TestScheduler_Enqueue(t *testing.T) {
    tests := []struct {
        name     string
        priority int
        wantPos  int
    }{
        {"high priority first", PriorityHigh, 0},
        {"medium priority second", PriorityMedium, 1},
        {"low priority last", PriorityLow, 2},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            s := NewScheduler()
            s.Enqueue(Task{Priority: tt.priority})
            // assertions...
        })
    }
}
```

### Key Areas for Unit Testing

| Package              | What to Test                                              |
|---------------------|-----------------------------------------------------------|
| `internal/wal`       | Entry serialization, CRC32 validation, segment rotation   |
| `internal/partition`  | Lease acquisition, epoch fencing, ownership transitions   |
| `internal/scheduler` | Priority ordering, WFQ ratios, worker selection scoring   |
| `internal/routing`   | Capability matching, locality scoring, load balancing     |
| `internal/config`    | Configuration parsing, defaults, validation               |
| `pkg/sdk`            | Handler registration, retry logic, rate limiting          |

### Test Conventions

- Use the `testing` package standard library
- Use `t.Helper()` for test helper functions
- Use `t.Parallel()` where safe for faster test execution
- Always run with `-race` flag to detect data races
- Name test functions as `Test<Type>_<Method>` (e.g., `TestWAL_Append`)

---

## Integration Testing

Integration tests verify interactions between multiple components within the Ortrix system.

### Orchestrator + Worker Interaction

Test the full task lifecycle from submission to completion:

```
1. Start orchestrator instance
2. Connect worker with registered capabilities
3. Submit task via gateway
4. Verify task is dispatched to worker over gRPC stream
5. Worker executes and returns result
6. Verify result is persisted in WAL and returned to client
```

**Scenarios:**

- Single task dispatch and completion
- Multiple tasks to the same worker
- Multiple workers with different capabilities
- Task dispatch to the correct worker based on capability matching

### Partition Failover

Test partition recovery when an orchestrator instance fails:

```
1. Start two orchestrator instances (O1 owns P0-P3, O2 owns P4-P7)
2. Submit tasks to partitions on O1
3. Kill O1
4. Wait for lease expiry
5. Verify O2 acquires P0-P3
6. Verify O2 recovers state from WAL
7. Verify in-flight tasks are re-dispatched
8. Verify new tasks for P0-P3 are handled by O2
```

### WAL Recovery

Test state reconstruction from WAL after crash:

```
1. Start orchestrator, submit 1000 tasks
2. Verify WAL contains all task events
3. Kill orchestrator (unclean shutdown)
4. Restart orchestrator
5. Verify in-memory state matches pre-crash state
6. Verify snapshot + WAL replay produces correct task states
7. Verify no duplicate or missing tasks
```

### Integration Test Infrastructure

- Use `testing.T` with setup/teardown helpers
- Start components as in-process instances (no Docker required for basic integration tests)
- Use a dedicated test port range to avoid conflicts
- Clean up all resources (goroutines, files, ports) in `t.Cleanup()`

---

## End-to-End Testing

End-to-end tests verify complete workflow execution across all Ortrix components.

### Full Workflow Execution

Test a complete workflow from client submission through worker execution:

```
Client → Gateway → Orchestrator → Worker SDK → Result → Client
```

**Scenarios:**

- Synchronous task execution (client blocks until result)
- Asynchronous task execution (client polls for result)
- Multi-step workflow with saga compensation
- Task with large payload (external storage reference)

### Multi-Service Scenario

Test Ortrix orchestrating tasks across multiple independent services:

```
1. Deploy 3 services: payment-service, email-service, inventory-service
2. Each service embeds the Worker SDK with different capabilities
3. Submit a workflow that requires all three capabilities
4. Verify tasks are routed to the correct services
5. Verify results are collected and workflow completes
6. Verify ordering constraints (if any) are respected
```

### E2E Test Infrastructure

- Use Docker Compose or Kind (Kubernetes in Docker) for full cluster setup
- Test against real gRPC connections (not mocked)
- Include gateway, orchestrator, and multiple worker services
- Use health checks to wait for components to be ready

---

## Failure Testing

Failure tests verify that Ortrix handles abnormal conditions correctly.

### Node Crash

Simulate orchestrator node failure:

| Scenario                       | Expected Behavior                                    |
|-------------------------------|------------------------------------------------------|
| Orchestrator crash             | Partitions fail over to another orchestrator         |
| Orchestrator crash during WAL write | Partial entry detected by CRC, skipped on replay |
| Orchestrator crash during snapshot | Previous snapshot used, WAL replayed from there  |
| Multiple orchestrator crashes   | Remaining orchestrators absorb all partitions       |

### Worker Disconnect

Simulate worker failure and network issues:

| Scenario                       | Expected Behavior                                    |
|-------------------------------|------------------------------------------------------|
| Worker process crash           | Stream closes, tasks re-dispatched to other workers  |
| Worker heartbeat timeout       | Worker marked dead after 15s, tasks re-dispatched    |
| Worker crash mid-task          | Task times out, re-dispatched with retry counter     |
| Worker reconnect after crash   | Re-registers capabilities, receives new tasks        |

### Network Failure

Simulate network partitions and degradation:

| Scenario                       | Expected Behavior                                    |
|-------------------------------|------------------------------------------------------|
| Network partition (orch ↔ worker) | Heartbeat timeout, tasks re-dispatched           |
| Network partition (gateway ↔ orch) | Gateway retries with updated routing table       |
| Transient network blip          | gRPC reconnection with backoff, no task loss       |
| High latency network            | Tasks complete slowly but correctly                |

### Failure Test Implementation

- Use `context.CancelFunc` to simulate process crashes in integration tests
- Use network namespaces or iptables rules for network partition simulation in E2E tests
- Verify invariants after each failure: no lost tasks, no duplicate completions, correct state after recovery

---

## Load Testing

> **Note**: Load testing is a future milestone. The framework and benchmarks below define the target approach.

### Throughput Benchmarks

| Metric                     | Target                | Measurement Method              |
|---------------------------|----------------------|---------------------------------|
| Task dispatch throughput   | 10,000 tasks/sec     | Sustained load for 5 minutes    |
| WAL write throughput       | 50,000 entries/sec   | Sequential append benchmark     |
| Snapshot creation time     | < 500ms for 100K tasks| Time from trigger to completion |
| Recovery time              | < 1s for 10K events  | Time from start to ACTIVE state |

### Latency Measurement

| Metric                     | Target          | Percentile |
|---------------------------|----------------|------------|
| Task dispatch latency      | < 2ms          | P50        |
| Task dispatch latency      | < 5ms          | P99        |
| End-to-end task latency    | < 10ms         | P50        |
| End-to-end task latency    | < 50ms         | P99        |

### Load Test Infrastructure

- Use Go benchmark framework (`testing.B`) for micro-benchmarks
- Use a dedicated load generator for sustained throughput testing
- Run in isolated environment to avoid noisy-neighbor effects
- Collect CPU, memory, and goroutine profiles during load tests

---

## CI Requirements

### All Pull Requests Must Include Tests

- Every PR that adds or modifies functionality must include corresponding tests
- Test-only PRs (adding tests for existing code) are encouraged
- Documentation-only PRs are exempt from test requirements

### CI Pipeline

```
┌──────────┐    ┌──────────┐    ┌──────────┐    ┌──────────┐
│  Lint    │───▶│  Build   │───▶│  Test    │───▶│ Coverage │
│ (golangci│    │ (go build│    │ (go test │    │  Check   │
│  -lint)  │    │  ./...)  │    │ -race)   │    │  (≥80%)  │
└──────────┘    └──────────┘    └──────────┘    └──────────┘
```

### Coverage Enforcement

- **CI must fail if overall coverage drops below 80%**
- Coverage is measured with `go test -coverprofile=coverage.out ./...`
- Coverage report is generated with `go tool cover -func=coverage.out`
- Per-package coverage is tracked to identify under-tested areas

### Test Execution

```bash
# Run all tests with race detection
go test ./... -v -race -count=1

# Run with coverage
go test ./... -v -race -coverprofile=coverage.out

# Check coverage threshold
go tool cover -func=coverage.out | grep total | awk '{print $3}' | \
  sed 's/%//' | awk '{if ($1 < 80) exit 1}'
```

### Test Categories in CI

| Category      | Trigger        | Timeout | Required |
|--------------|----------------|---------|----------|
| Unit tests    | Every PR       | 5 min   | Yes      |
| Integration   | Every PR       | 10 min  | Yes      |
| E2E tests     | Merge to main  | 30 min  | Yes      |
| Failure tests | Nightly        | 60 min  | Advisory |
| Load tests    | Weekly/Manual  | 120 min | Advisory |
