# Flowd

**A low-latency, Kubernetes-native distributed workflow orchestrator.**

Flowd is built on partitioned execution, push-based gRPC streaming, and locality-aware scheduling. It delivers sub-millisecond task dispatch — orders of magnitude faster than poll-based systems.

---

## Key Features

- **Push-based execution** — Tasks are streamed to workers instantly via persistent gRPC connections. No polling, no queue consumption delays.
- **Partitioned execution model** — `hash(workflow_id) → partition → single owner`. No distributed locks on the hot path.
- **Event-sourced WAL** — Durable, replayable state with hybrid local/replicated write-ahead log.
- **Embedded worker SDK** — No separate worker services. Import the SDK into your existing Go services and declare capabilities.
- **Capability-based routing** — Workers self-declare what they can do. The orchestrator routes dynamically.
- **Locality-aware scheduling** — Prefer same-pod → same-node → same-zone → any, minimizing network hops.
- **Priority queues with fairness** — HIGH / MEDIUM / LOW with weighted fair queuing to prevent starvation.
- **Saga pattern support** — Built-in compensation for multi-step workflows.
- **At-least-once execution** — Idempotency guarantees for safe retries.
- **Canary and blue-green deployments** — Version-aware routing for safe rollouts.
- **Fast recovery** — Snapshot + WAL replay reconstructs state in hundreds of milliseconds.

---

## Architecture

```
  Client ──▶ Gateway (control plane: auth, routing)
                │
                ▼
          Orchestrator (partitioned, in-memory + WAL)
                │
           gRPC stream (push)
                │
                ▼
          Your Service (embedded Worker SDK)
```

**Control Plane (Gateway)**: Handles bootstrap, authentication, and routing metadata. Not in the execution path.

**Data Plane (Orchestrator ↔ Workers)**: All task dispatch and result collection flows directly over persistent gRPC streams. No intermediate hops, no polling.

**Workers**: The SDK embeds into your services. Register task handlers, connect to the orchestrator, and receive tasks instantly.

See [docs/architecture.md](docs/architecture.md) for the full design.

---

## Why Flowd

Traditional workflow engines use **poll-based** task dispatch. Workers repeatedly ask "any work for me?" — adding 500ms+ of latency on every task and wasting resources on empty polls.

Flowd eliminates this with **push-based streaming**:

| Metric              | Poll-based (e.g., Temporal) | Flowd (push)   |
|--------------------|----------------------------|-----------------|
| P50 dispatch       | ~500ms                     | ~1ms            |
| P99 dispatch       | ~1000ms                    | ~5ms            |
| Idle overhead      | Continuous polling          | Zero            |

Flowd also eliminates the need for:
- **External databases** — WAL provides durability without Cassandra/Postgres
- **Separate worker infrastructure** — SDK embeds into existing services
- **Complex deployment** — Kubernetes-native from day one

---

## Comparison with Existing Systems

| Dimension        | Flowd                        | Temporal                      |
|-----------------|------------------------------|-------------------------------|
| Dispatch model  | Push (gRPC streaming)        | Pull (long polling)           |
| Dispatch latency| ~1ms                         | ~500ms                        |
| State storage   | In-memory + WAL              | External database             |
| Workers         | Embedded SDK                 | Separate processes            |
| Routing         | Capability + locality aware  | Task queue based              |
| Best for        | Low-latency, high-throughput | Rich workflow semantics       |

See [docs/comparison.md](docs/comparison.md) for a detailed analysis.

---

## Quick Start

### Prerequisites

- Go 1.24+
- protoc (for proto generation)

### Build

```bash
make build
```

### Run Locally

```bash
# Terminal 1: Start the orchestrator
make run-orchestrator

# Terminal 2: Start the gateway
make run-gateway
```

### Embed the Worker SDK

```go
package main

import (
    "context"
    "github.com/mayur-tolexo/flowd/pkg/sdk"
)

func main() {
    w := sdk.NewWorker("my-service")
    w.RegisterHandler("process_order", func(ctx context.Context, taskID string, payload []byte) ([]byte, error) {
        // Your task logic here
        return []byte(`{"status": "done"}`), nil
    })
    w.Start(context.Background(), "localhost:9090")
}
```

### Run on Kubernetes

```bash
# Create a local cluster
make kind-create

# Build Docker images
make docker-all

# Deploy with Helm (coming soon)
```

---

## Repository Structure

```
flowd/
├── api/proto/           # gRPC/Protobuf service definitions
├── cmd/
│   ├── gateway/         # Gateway service entry point
│   └── orchestrator/    # Orchestrator service entry point
├── internal/
│   ├── config/          # Configuration management
│   ├── logging/         # Structured logging
│   ├── partition/       # Partition ownership and management
│   ├── routing/         # Task routing logic
│   ├── scheduler/       # Priority scheduling
│   └── wal/             # Write-ahead log
├── pkg/sdk/             # Worker SDK (public API)
├── deploy/
│   ├── helm/            # Helm charts
│   └── k8s/             # Kubernetes manifests
└── docs/                # Design documentation
```

---

## Documentation

| Document | Description |
|----------|-------------|
| [Architecture](docs/architecture.md) | System design, control vs data plane, partition model |
| [Execution Model](docs/execution-model.md) | Push-based dispatch, gRPC streaming, task lifecycle |
| [State and WAL](docs/state-and-wal.md) | Event sourcing, snapshots, recovery, large payloads |
| [Partitioning and Scaling](docs/partitioning-and-scaling.md) | Leases, rebalancing, failover, horizontal scaling |
| [Scheduling and Routing](docs/scheduling-and-routing.md) | Capability routing, locality scheduling, priority queues |
| [Failure Handling](docs/failure-handling.md) | Crash recovery, idempotency, saga compensation |
| [Security](docs/security.md) | mTLS, service identity, authorization |
| [Performance](docs/performance.md) | Latency analysis, batching, WAL optimization |
| [Comparison](docs/comparison.md) | Flowd vs Temporal |

---

## Contributing

We welcome contributions! See [CONTRIBUTING.md](CONTRIBUTING.md) for:

- How to run locally
- How to add a new capability
- Coding standards
- Testing expectations
- PR guidelines

---

## License

See [LICENSE](LICENSE) for details.
