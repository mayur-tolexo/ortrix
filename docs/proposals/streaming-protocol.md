# Proposal: Streaming Protocol for Worker Communication

## Table of Contents

- [Status](#status)
- [Summary](#summary)
- [Motivation](#motivation)
- [Protocol Definition](#protocol-definition)
  - [RPC Signature](#rpc-signature)
  - [Message Types](#message-types)
- [Capacity Signaling](#capacity-signaling)
  - [Registration Capacity](#registration-capacity)
  - [Dynamic Capacity Updates](#dynamic-capacity-updates)
  - [Orchestrator Capacity Tracking](#orchestrator-capacity-tracking)
- [Flow Control](#flow-control)
  - [Application-Level Flow Control](#application-level-flow-control)
  - [Transport-Level Flow Control](#transport-level-flow-control)
  - [Backpressure Propagation](#backpressure-propagation)
- [Connection Lifecycle](#connection-lifecycle)
  - [Establishment](#establishment)
  - [Steady State](#steady-state)
  - [Graceful Shutdown](#graceful-shutdown)
  - [Ungraceful Disconnection](#ungraceful-disconnection)
- [Failure Handling](#failure-handling)
  - [Stream Disconnection](#stream-disconnection)
  - [Worker Reconnection](#worker-reconnection)
  - [Task Timeout](#task-timeout)
  - [Duplicate Result Handling](#duplicate-result-handling)
  - [Orchestrator Failover](#orchestrator-failover)
- [Configuration](#configuration)
- [Security Considerations](#security-considerations)

---



## Status

**Accepted** — This proposal defines the streaming protocol used for all orchestrator ↔ worker communication in Ortrix.

## Summary

Ortrix uses a bidirectional gRPC streaming protocol between orchestrators and workers. Workers initiate outbound connections to orchestrators. All task dispatch, result collection, heartbeats, and capacity signaling flow over a single long-lived stream per worker. This proposal defines the protocol semantics, flow control, connection lifecycle, and failure handling.

## Motivation

Traditional workflow engines use pull-based dispatch (workers poll a queue). This introduces polling latency, wasted resources on empty polls, and scaling tradeoffs between latency and load. Ortrix replaces this with push-based streaming, but a push model requires careful protocol design to avoid overwhelming workers.

This proposal ensures that:
- Task dispatch is near-instantaneous (~1ms vs ~500ms for polling)
- Workers are never overloaded beyond their declared capacity
- Connection failures are handled gracefully with minimal task impact
- The protocol is simple enough to implement in any language

## Protocol Definition

### RPC Signature

```protobuf
service WorkerService {
  rpc StreamTasks(stream WorkerMessage) returns (stream OrchestratorMessage);
}
```

### Message Types

**WorkerMessage** (Worker → Orchestrator):

| Field           | Type              | Description                              |
|-----------------|-------------------|------------------------------------------|
| registration    | WorkerRegistration| Initial capabilities + capacity          |
| task_result     | TaskResult        | Completed task result                    |
| heartbeat       | Heartbeat         | Liveness signal                          |
| capacity_update | CapacityUpdate    | Updated available slots                  |

**OrchestratorMessage** (Orchestrator → Worker):

| Field   | Type | Description                              |
|---------|------|------------------------------------------|
| task    | Task | Task to execute                          |
| ack     | Ack  | Acknowledgment of worker message         |

## Capacity Signaling

Workers control how many tasks they can handle concurrently. The orchestrator **must** respect this limit.

### Registration Capacity

On connection, the worker sends its maximum concurrent capacity:

```
WorkerRegistration {
  worker_id: "svc-a-pod-xyz"
  capabilities: ["process_payment", "refund_payment"]
  max_concurrent: 10
  metadata: {
    node: "node-3"
    zone: "us-east-1a"
  }
}
```

### Dynamic Capacity Updates

Workers can update their capacity at any time by sending a `CapacityUpdate` message:

```
CapacityUpdate {
  available_slots: 5    // Reduced from 10 due to memory pressure
  reason: "memory_pressure"
}
```

Use cases for dynamic capacity updates:
- **Memory pressure**: Worker detects high memory usage and reduces capacity
- **Downstream degradation**: Worker's downstream dependency is slow, reducing throughput
- **Deployment drain**: Worker is shutting down and wants to stop receiving new tasks
- **Recovery**: Worker recovers from a transient issue and increases capacity

### Orchestrator Capacity Tracking

The orchestrator maintains a capacity counter per worker:

```
available_slots = max_concurrent - in_flight_tasks

On task dispatch:    in_flight_tasks++, available_slots--
On task result:      in_flight_tasks--, available_slots++
On capacity update:  available_slots = message.available_slots
```

**Invariant**: The orchestrator never dispatches a task to a worker with `available_slots <= 0`.

## Flow Control

### Application-Level Flow Control

Ortrix implements application-level flow control on top of gRPC:

```
  ┌─────────────┐                          ┌───────────────┐
  │   Worker     │                          │ Orchestrator   │
  ├─────────────┤                          ├────────────────┤
  │             │──REGISTER(cap=10)──────▶│  slots[w]=10   │
  │             │                          │                │
  │             │◀──Task(t1)─────────────│  slots[w]=9    │
  │             │◀──Task(t2)─────────────│  slots[w]=8    │
  │             │                          │                │
  │             │──Result(t1)────────────▶│  slots[w]=9    │
  │             │                          │                │
  │             │──CapUpdate(slots=3)────▶│  slots[w]=3    │
  │             │                          │  (won't exceed)│
  │             │                          │                │
  │  (0 slots)  │   ◀── no tasks sent ──  │  slots[w]=0    │
  │             │                          │                │
  │             │──Result(t2)────────────▶│  slots[w]=1    │
  │             │◀──Task(t3)─────────────│  slots[w]=0    │
  └─────────────┘                          └────────────────┘
```

### Transport-Level Flow Control

gRPC (HTTP/2) provides transport-level flow control via window updates. If a worker's receive buffer fills up (e.g., slow handler execution), HTTP/2 flow control automatically slows the sender. This acts as a safety net below the application-level protocol.

### Backpressure Propagation

When all workers for a given capability are at capacity:

1. Tasks remain in the orchestrator's priority queue
2. No dispatch attempts are made for that capability
3. Queue depth metrics are exposed (`ortrix_queue_depth{capability="..."}`)
4. Auto-scaling systems can react to growing queue depth

## Connection Lifecycle

### Establishment

```
  Worker SDK                              Orchestrator
    │                                         │
    │──TCP connect─────────────────────────▶│
    │──TLS handshake (mTLS)───────────────▶│
    │  (both sides verify certificates)     │
    │──HTTP/2 connection established──────▶│
    │──StreamTasks RPC opened─────────────▶│
    │──WorkerRegistration message──────────▶│
    │   {id, capabilities, max_concurrent}  │
    │◀──Ack──────────────────────────────│
    │                                         │
    │   === stream is now active ===          │
```

### Steady State

During normal operation, the stream carries interleaved messages:

```
  Time →
    W→O: Heartbeat
    O→W: Task(t1)
    O→W: Task(t2)
    W→O: Result(t1)
    W→O: Heartbeat
    O→W: Task(t3)
    W→O: Result(t2)
    W→O: CapacityUpdate(slots=5)
    W→O: Result(t3)
    W→O: Heartbeat
```

Heartbeats are sent at a configurable interval (default: 10s). The orchestrator considers a worker dead if no message is received within the heartbeat timeout (default: 30s).

### Graceful Shutdown

When a worker is shutting down (e.g., pod termination):

```
  1. Worker sends CapacityUpdate { available_slots: 0 }
     → Orchestrator stops sending new tasks
  2. Worker completes all in-flight tasks
     → Sends TaskResult for each
  3. Worker closes the stream
     → Orchestrator removes worker from capability index
```

This ensures zero task loss during rolling deployments.

### Ungraceful Disconnection

When a worker disconnects unexpectedly (crash, node failure, network partition):

```
  1. Orchestrator detects stream EOF or heartbeat timeout
  2. Orchestrator marks all in-flight tasks for that worker as FAILED
  3. Failed tasks are re-enqueued (if within retry limit)
  4. Worker is removed from the capability index
  5. If worker reconnects, it goes through full registration again
```

## Failure Handling

### Stream Disconnection

| Failure                    | Detection               | Recovery                           |
|---------------------------|-------------------------|------------------------------------|
| Worker crash              | Stream EOF              | Re-enqueue in-flight tasks         |
| Network partition         | Heartbeat timeout (30s) | Mark worker dead, re-enqueue tasks |
| Orchestrator crash        | Stream EOF (worker-side)| Worker reconnects to new orchestrator |
| TLS certificate expiry    | Handshake failure       | Worker retries after cert rotation |

### Worker Reconnection

The Worker SDK implements automatic reconnection with exponential backoff:

```
  Attempt 1: wait 100ms  → connect
  Attempt 2: wait 200ms  → connect
  Attempt 3: wait 400ms  → connect
  Attempt 4: wait 800ms  → connect
  ...
  Max backoff: 30s
  Jitter: ±25% (prevents thundering herd on orchestrator restart)
```

On reconnection, the worker sends a fresh `WorkerRegistration`. The orchestrator treats it as a new connection — no state is assumed from the previous stream.

### Task Timeout

If a worker does not return a result within the task deadline:

```
  1. Orchestrator marks the task as FAILED (timeout)
  2. Task is re-enqueued if within retry limit
  3. Worker's in-flight count is decremented
  4. If the worker later sends a result for the timed-out task, it is ignored
```

### Duplicate Result Handling

Results for already-completed or timed-out tasks are silently discarded. Task state transitions are guarded by the WAL — only valid transitions are recorded.

### Orchestrator Failover

When an orchestrator instance fails:

```
  1. Partition lease expires (not renewed)
  2. Another orchestrator acquires the lease
  3. New owner replays WAL to reconstruct state
  4. Workers detect stream disconnect and reconnect
  5. Workers may connect to a different orchestrator
     (discovered via gateway routing metadata)
  6. In-flight tasks from the failed orchestrator are
     recovered from WAL in DISPATCHED state and re-enqueued
```

## Configuration

| Parameter                     | Default | Description                                |
|-------------------------------|---------|---------------------------------------------|
| `worker.heartbeat_interval`   | 10s     | How often workers send heartbeats           |
| `worker.heartbeat_timeout`    | 30s     | Time before marking a worker as dead        |
| `worker.max_concurrent`       | 100     | Default max concurrent tasks per worker     |
| `worker.reconnect_backoff`    | 100ms   | Initial reconnection backoff                |
| `worker.reconnect_max`        | 30s     | Maximum reconnection backoff                |
| `worker.reconnect_jitter`     | 0.25    | Jitter factor for reconnection timing       |
| `task.default_timeout`        | 5m      | Default task execution timeout              |
| `stream.max_message_size`     | 4MB     | Maximum gRPC message size                   |

## Security Considerations

- All streams use mTLS — no plaintext connections are allowed
- Worker identity is extracted from the TLS certificate, not self-reported
- Capability authorization is checked on every registration
- Reconnecting workers must re-authenticate (no session resumption)
- Network policies should restrict worker egress to orchestrator pods only
