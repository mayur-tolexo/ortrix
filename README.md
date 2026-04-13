# Ortrix

**A low-latency, Kubernetes-native distributed workflow orchestrator.**

Ortrix is a low-latency, Kubernetes-native distributed workflow orchestrator built on partitioned execution, streaming task dispatch, and locality-aware scheduling. It delivers sub-millisecond task dispatch — orders of magnitude faster than poll-based systems.

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

## Why Ortrix

Traditional workflow engines use **poll-based** task dispatch. Workers repeatedly ask "any work for me?" — adding 500ms+ of latency on every task and wasting resources on empty polls.

Ortrix eliminates this with **push-based streaming**:

| Metric              | Poll-based (e.g., Temporal) | Ortrix (push)   |
|--------------------|----------------------------|-----------------|
| P50 dispatch       | ~500ms                     | ~1ms            |
| P99 dispatch       | ~1000ms                    | ~5ms            |
| Idle overhead      | Continuous polling          | Zero            |

Ortrix also eliminates the need for:
- **External databases** — WAL provides durability without Cassandra/Postgres
- **Separate worker infrastructure** — SDK embeds into existing services
- **Complex deployment** — Kubernetes-native from day one

---

## Comparison with Existing Systems

| Dimension        | Ortrix                       | Temporal                      |
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
    "github.com/mayur-tolexo/ortrix/pkg/sdk"
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
ortrix/
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

### Documentation Structure

| Document | Description |
|----------|-------------|
| [Architecture](docs/architecture.md) | System design, control vs data plane, partition model |
| [Execution Model](docs/execution-model.md) | Push-based dispatch, gRPC streaming, task lifecycle |
| [State and WAL](docs/state-and-wal.md) | Event sourcing, WAL format, snapshots, recovery |
| [Scheduling and Routing](docs/scheduling-and-routing.md) | Capability routing, locality scheduling, priority queues |
| [Failure Handling](docs/failure-handling.md) | Crash recovery, idempotency, saga compensation |
| [Partitioning and Scaling](docs/partitioning-and-scaling.md) | Leases, rebalancing, failover, horizontal scaling |
| [Security](docs/security.md) | mTLS, service identity, authorization |
| [Performance](docs/performance.md) | Latency analysis, batching, WAL optimization |
| [Comparison](docs/comparison.md) | Ortrix vs Temporal |
| [Future Work](docs/future-work.md) | Research directions: rebalancing, replication, multi-region |
| [Proposals](docs/proposals/) | Design proposals for upcoming features |
| [Roadmap](docs/roadmap.md) | Phased development plan with goals and deliverables |
| [Testing](docs/testing.md) | Testing strategy, coverage requirements, CI enforcement |

### Development Approach

Ortrix follows a **proposal-driven development** model with **phased delivery** and **strong testing discipline**:

- **Proposal-driven development**: Every significant feature begins as a written proposal in [`docs/proposals/`](docs/proposals/). Proposals define the problem, motivation, design, alternatives, tradeoffs, testing strategy, and rollout plan. This ensures architectural decisions are deliberate, reviewed, and documented before implementation begins.

- **Phased delivery**: The [roadmap](docs/roadmap.md) defines six development phases, each building on the previous. Each phase delivers a functional, testable system with clear goals, deliverables, and dependencies. This enables incremental progress with confidence.

- **Strong testing discipline**: The [testing strategy](docs/testing.md) mandates 80%+ code coverage, table-driven unit tests, integration tests for component interactions, and failure tests for crash recovery scenarios. All PRs must include tests, and CI fails if coverage drops below the threshold.

---

## 🚧 Future Work

Ortrix is actively evolving. Key areas of upcoming development:

- **Locality-aware partition migration** — Move partitions closer to their execution zones, reducing cross-zone latency and egress costs
- **Load-based rebalancing** — Detect hot orchestrator nodes and automatically redistribute partitions based on CPU, queue depth, and latency
- **Hot partition mitigation** — Sub-partitioning and key-based sharding to break up workflow hotspots
- **Partition replication & fast failover** — Warm standby nodes with continuous WAL streaming for near-instant promotion
- **Multi-region support** — Geo-distributed orchestration with cross-region WAL replication and global routing

See [docs/future-work.md](docs/future-work.md) for the full roadmap with design details, ASCII diagrams, and tradeoff analysis.

**Want to contribute?** These are excellent areas for new contributors. Check the roadmap and pick an area that interests you.

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
