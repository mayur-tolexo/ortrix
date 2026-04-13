# Flowd Architecture

## High-Level Overview

Flowd is a Kubernetes-native distributed workflow orchestrator designed for low-latency, high-throughput task execution. It separates the **control plane** from the **data plane** to minimize overhead on the critical execution path.

```
                         ┌─────────────────────────────────────────────┐
                         │              Control Plane                  │
                         │                                             │
  ┌──────────┐    ┌──────┴───────┐                                     │
  │  Client   │───▶│   Gateway    │  bootstrap · auth · routing meta   │
  └──────────┘    └──────┬───────┘                                     │
                         └─────────────────────────────────────────────┘
                                          │
                            routing metadata (partition → owner)
                                          │
                         ┌────────────────▼────────────────────────────┐
                         │              Data Plane                     │
                         │                                             │
                         │  ┌──────────────┐    ┌──────────────┐       │
                         │  │ Orchestrator  │    │ Orchestrator  │      │
                         │  │  (Partition   │    │  (Partition   │      │
                         │  │   Owner 0-3)  │    │   Owner 4-7)  │     │
                         │  └──────┬───────┘    └──────┬───────┘       │
                         │         │                   │               │
                         │    gRPC Streaming       gRPC Streaming      │
                         │         │                   │               │
                         │  ┌──────▼───────┐    ┌──────▼───────┐       │
                         │  │   Service A   │    │   Service B   │     │
                         │  │ (Worker SDK)  │    │ (Worker SDK)  │     │
                         │  └──────────────┘    └──────────────┘       │
                         └─────────────────────────────────────────────┘
```

## Control Plane vs Data Plane

### Control Plane (Gateway)

The Gateway handles **administrative operations only**. It is explicitly **not** in the task execution path.

| Responsibility       | Description                                    |
|----------------------|------------------------------------------------|
| Bootstrap            | Initial service registration and discovery     |
| Authentication       | mTLS termination, token validation             |
| Routing Metadata     | Resolve `hash(workflow_id) → partition → owner`|
| Task Submission      | Accept tasks and forward to correct partition  |
| Status Queries       | Proxy status requests to partition owners      |

### Data Plane (Orchestrator ↔ Workers)

All task execution flows directly between orchestrator instances and worker services via **persistent gRPC bidirectional streams**. The gateway is never involved in task dispatch or result collection.

| Responsibility       | Description                                    |
|----------------------|------------------------------------------------|
| Task Dispatch        | Push tasks to workers via gRPC stream          |
| Result Collection    | Receive results on the same stream             |
| State Management     | WAL writes, snapshots, in-memory state         |
| Heartbeat            | Worker liveness tracking                       |

## Gateway Role

The gateway is intentionally thin:

```
  Client ──▶ Gateway ──▶ "Partition 5 is owned by Orchestrator-2 at 10.0.3.4:9090"
                              │
                              ▼
  Client ──────────────────▶ Orchestrator-2 (direct connection for all subsequent calls)
```

After initial routing resolution, clients and services communicate **directly** with the owning orchestrator. This eliminates the gateway as a bottleneck and reduces latency by one network hop on the hot path.

## Partition Model

Flowd uses a **hash-based partitioning** scheme:

```
  partition_id = hash(workflow_id) % num_partitions
```

Each partition is owned by **exactly one** orchestrator instance at any given time. Ownership is maintained through a distributed lease mechanism.

```
  ┌──────────────────────────────────────────┐
  │            Partition Table                │
  │                                          │
  │  Partition 0  →  Orchestrator-1 (lease)  │
  │  Partition 1  →  Orchestrator-1 (lease)  │
  │  Partition 2  →  Orchestrator-2 (lease)  │
  │  Partition 3  →  Orchestrator-2 (lease)  │
  │  Partition 4  →  Orchestrator-3 (lease)  │
  │  ...                                     │
  └──────────────────────────────────────────┘
```

Key properties:

- **Single writer**: One orchestrator owns each partition — no distributed locks on the hot path
- **Lease-based**: Ownership expires if not renewed, enabling automatic failover
- **Rebalanceable**: Partitions can be moved between orchestrators for load balancing
- **Deterministic**: Any node can compute `workflow_id → partition` independently

## Worker SDK Model

Workers are **not** standalone services. The Flowd Worker SDK is embedded directly inside your existing services:

```go
import "github.com/mayur-tolexo/flowd/pkg/sdk"

func main() {
    // Your existing service setup...

    w := sdk.NewWorker("payment-service")
    w.RegisterHandler("process_payment", handlePayment)
    w.RegisterHandler("refund_payment", handleRefund)

    // Connects to orchestrator, streams tasks
    w.Start(ctx, "orchestrator:9090")

    // Your existing service continues running...
}
```

The SDK:

1. **Registers capabilities** (task types the service can handle)
2. **Opens a gRPC stream** to the orchestrator
3. **Receives tasks** pushed by the orchestrator
4. **Executes handlers** and streams results back
5. **Sends heartbeats** for liveness detection

This model eliminates the need for separate worker infrastructure. Any service that imports the SDK becomes a Flowd worker, maintaining its own identity and lifecycle.

## Component Summary

| Component      | Port  | Role                                      |
|---------------|-------|-------------------------------------------|
| Gateway        | 8080  | Control plane: auth, routing, submission  |
| Orchestrator   | 9090  | Data plane: dispatch, WAL, state mgmt    |
| Worker SDK     | —     | Embedded in services, no separate port    |

## Data Flow

### Task Submission (Full Path)

```
1. Client ──▶ Gateway.SubmitTask(task)
2. Gateway: partition = hash(task.workflow_id) % N
3. Gateway: owner = partition_table[partition]
4. Gateway ──▶ Orchestrator(owner).Enqueue(task)
5. Orchestrator: WAL.Append(TaskCreated)
6. Orchestrator: scheduler.Enqueue(task, priority)
7. Orchestrator: select worker by capability + locality
8. Orchestrator ──stream──▶ Worker.Execute(task)
9. Worker ──stream──▶ Orchestrator.TaskResult(result)
10. Orchestrator: WAL.Append(TaskCompleted)
11. Orchestrator: update in-memory state
```

### Task Execution (Steady State)

Once a worker is connected, the data plane operates without any gateway involvement:

```
  Orchestrator ──push──▶ Worker (via existing stream)
  Worker ──result──▶ Orchestrator (via existing stream)
```

No polling. No queue consumption delays. No gateway hop.

## Future Evolution

Flowd's current architecture provides a solid foundation. The following areas represent the next evolution of the system:

### Adaptive Partition Placement

Today, partitions are assigned to orchestrators using hash-based distribution. Future work will introduce **locality-aware placement** — tracking where tasks are actually executed and migrating partitions closer to their execution zones. This reduces cross-zone network hops and cloud egress costs. Placement hints (region, tenant, service affinity) will allow workflows to influence their initial assignment.

### Load-Based Balancing

Hash-based partitioning distributes workflows uniformly by key but not by load. Load-based rebalancing will monitor per-node CPU, queue depth, and dispatch latency, then redistribute partitions from hot nodes to cold nodes. Hot partition mitigation via sub-partitioning will handle cases where a single workflow creates disproportionate load.

### Partition Replication

Current failover requires lease expiry followed by full WAL replay. Warm standby replication will continuously stream WAL entries to a follower node, enabling near-instant promotion on failure. This reduces failover time from seconds to milliseconds for latency-sensitive workloads.

See [Future Work](future-work.md) for the complete roadmap.
