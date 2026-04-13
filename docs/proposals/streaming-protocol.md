# Proposal: Streaming Protocol

## Table of Contents

- [Status](#status)
- [Problem Statement](#problem-statement)
- [Motivation](#motivation)
- [Design Details](#design-details)
  - [gRPC Bidirectional Streaming](#grpc-bidirectional-streaming)
  - [Connection Lifecycle](#connection-lifecycle)
  - [Backpressure Handling](#backpressure-handling)
- [Alternatives Considered](#alternatives-considered)
- [Tradeoffs](#tradeoffs)
- [Testing Strategy](#testing-strategy)
- [Rollout Plan](#rollout-plan)

---

## Status

**Proposed**

---

## Problem Statement

Ortrix uses persistent gRPC bidirectional streams between orchestrators and workers for task dispatch and result collection. The streaming protocol needs a well-defined specification covering connection establishment, message framing, lifecycle management, error handling, and backpressure to ensure reliable operation under varying network conditions and load patterns.

---

## Motivation

- **Reliability**: Without a well-defined connection lifecycle, edge cases (reconnection storms, half-open connections, stale streams) cause task loss or duplicate dispatch.
- **Performance**: Backpressure handling prevents orchestrators from overwhelming slow workers, avoiding cascading failures.
- **Operability**: A clear protocol specification makes it possible to implement compatible SDKs in multiple languages and debug streaming issues systematically.

---

## Design Details

### gRPC Bidirectional Streaming

The core streaming interface uses gRPC bidirectional streaming:

```protobuf
service WorkerService {
  rpc StreamTasks(stream WorkerMessage) returns (stream OrchestratorMessage);
}
```

**Worker → Orchestrator (WorkerMessage):**

| Message Type         | Purpose                            |
|---------------------|------------------------------------|
| WorkerRegistration   | Announce capabilities on connect   |
| TaskResult           | Return task execution result       |
| Heartbeat            | Liveness signal with load metrics  |

**Orchestrator → Worker (OrchestratorMessage):**

| Message Type | Purpose                              |
|-------------|--------------------------------------|
| Task         | Push a task for execution            |
| Ack          | Acknowledge receipt of worker msg    |
| DrainSignal  | Request graceful worker drain        |

**Message ordering guarantees:**

- Messages within a single stream are delivered in order (gRPC guarantee over HTTP/2)
- The orchestrator does not send tasks until the initial WorkerRegistration is acknowledged
- TaskResult messages reference specific task IDs for correlation

### Connection Lifecycle

```
┌──────────────┐                          ┌───────────────┐
│   Worker      │                          │ Orchestrator   │
│   (SDK)       │                          │                │
├──────────────┤                          ├────────────────┤
│ CONNECTING   │──TLS Handshake─────────▶│                │
│              │──HTTP/2 Setup──────────▶│                │
│              │──StreamTasks()─────────▶│  STREAM_OPEN   │
│              │                          │                │
│ REGISTERING  │──WorkerRegistration────▶│  validate      │
│              │  {id, capabilities}      │  capabilities  │
│              │◀──Ack──────────────────│                │
│ ACTIVE       │                          │  WORKER_READY  │
│              │                          │                │
│              │◀──Task(s)──────────────│  dispatching   │
│              │──TaskResult────────────▶│                │
│              │──Heartbeat─────────────▶│                │
│              │                          │                │
│ DRAINING     │◀──DrainSignal─────────│  DRAINING      │
│              │  (finish current tasks)  │                │
│              │──TaskResult (final)────▶│                │
│ DISCONNECTED │──stream close──────────▶│  DISCONNECTED  │
└──────────────┘                          └───────────────┘
```

**Connection states:**

| State         | Description                                             |
|---------------|---------------------------------------------------------|
| CONNECTING    | TLS handshake and HTTP/2 setup in progress              |
| REGISTERING   | Stream open, sending WorkerRegistration                 |
| ACTIVE        | Fully connected, sending/receiving tasks                |
| DRAINING      | Finishing in-flight tasks, not accepting new ones       |
| DISCONNECTED  | Stream closed, cleanup in progress                      |

**Reconnection policy:**

- Exponential backoff starting at 100ms, max 30s
- Jitter added to prevent reconnection storms
- Max reconnection attempts configurable (default: unlimited)
- On reconnect, worker re-sends WorkerRegistration to rebuild state

**Keepalive and liveness:**

- gRPC keepalive pings every 30s (transport-level)
- Application-level heartbeats every 5s from worker
- Orchestrator marks worker as dead after 3 missed heartbeats (15s)

### Backpressure Handling

Backpressure is implemented at three layers:

**Layer 1: gRPC flow control (HTTP/2 window)**

- HTTP/2 flow control limits in-flight bytes per stream
- If the worker stops reading, the orchestrator's send buffer fills and writes block
- Prevents unbounded memory growth on both sides

**Layer 2: Application-level capacity reporting**

```
Heartbeat {
  timestamp:     int64
  active_tasks:  int32   // currently executing
  max_capacity:  int32   // max concurrent tasks
  queue_depth:   int32   // local queue size
}
```

- The orchestrator tracks each worker's capacity from heartbeat data
- Dispatch is skipped for workers at or above capacity
- Workers can dynamically adjust max_capacity based on resource usage

**Layer 3: Orchestrator-to-client backpressure**

- When partition queue depth exceeds threshold, the orchestrator returns `RESOURCE_EXHAUSTED` to the gateway
- Gateway propagates rejection to clients with a `Retry-After` header
- Prevents overloading the system from the ingress side

**Adaptive dispatch rate:**

```
dispatch_rate(worker) = min(
    worker.max_capacity - worker.active_tasks,
    partition_queue_limit - partition_queue_depth,
    global_rate_limit
)
```

---

## Alternatives Considered

| Alternative             | Pros                              | Cons                                     | Why Not                                      |
|------------------------|-----------------------------------|------------------------------------------|----------------------------------------------|
| Unary RPCs per task     | Simpler, stateless                | Connection overhead per task, no push     | Defeats push-based model                     |
| WebSocket streaming     | Browser-compatible                | No built-in flow control, less typed      | Workers are services, not browsers           |
| Custom TCP protocol     | Maximum control                   | Huge implementation effort, no ecosystem  | gRPC provides everything needed              |

---

## Tradeoffs

- **Persistent connections vs statelessness**: Persistent streams enable push-based dispatch but require connection management and make load balancing harder (connections are sticky).
- **Application heartbeats vs transport keepalive**: Application heartbeats carry load metadata but add message overhead. Transport keepalive is lightweight but carries no application data.
- **Strict backpressure vs best-effort**: Strict backpressure prevents overload but can cause upstream queuing. Best-effort dispatch is simpler but risks cascading failure.

---

## Testing Strategy

- **Unit tests**: Test message serialization, connection state machine transitions, backpressure calculations
- **Integration tests**: Orchestrator + worker streaming end-to-end, verify task dispatch and result collection over real gRPC streams
- **Failure tests**: Stream disconnection during task dispatch, reconnection under load, half-open connection detection
- **Backpressure tests**: Overwhelm a worker and verify dispatch rate adapts, verify `RESOURCE_EXHAUSTED` propagation to clients
- **Longevity tests**: Run streams for extended periods, verify no resource leaks (goroutines, file descriptors, memory)

---

## Rollout Plan

1. **Phase 1**: Implement core bidirectional streaming with registration, task dispatch, and result collection. Deploy with basic heartbeat-based liveness detection.
2. **Phase 2**: Add capacity-aware backpressure. Workers report load in heartbeats, orchestrator adjusts dispatch rate accordingly.
3. **Phase 3**: Add drain signal support for graceful shutdown. Implement reconnection with exponential backoff and jitter.
