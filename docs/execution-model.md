# Execution Model

## Sync vs Async Execution

Ortrix supports both synchronous and asynchronous task execution modes:

### Synchronous

The caller blocks until the task completes. Used for latency-sensitive operations where the result is needed immediately.

```
Client ──▶ SubmitTask(sync=true) ──▶ blocks ──▶ Result
```

- Gateway holds the connection open
- Orchestrator dispatches and waits for the worker result
- Result is returned on the same request path
- Timeout-bounded to prevent indefinite blocking

### Asynchronous

The caller receives a task ID immediately and polls or subscribes for the result later.

```
Client ──▶ SubmitTask(sync=false) ──▶ task_id (immediate)
Client ──▶ GetTaskStatus(task_id) ──▶ status/result (later)
```

- Default mode for most workflows
- Enables fire-and-forget patterns
- Supports long-running tasks
- Status can be queried at any time

## Push vs Pull: Why Push

Ortrix uses a **push-based** execution model. Tasks are pushed from the orchestrator to workers over persistent gRPC streams. This is a deliberate architectural choice.

### Pull Model (rejected)

```
Worker ──poll──▶ Queue ──▶ "any tasks for me?"
                         ──▶ "no"     (wasted round trip)
                         ──▶ "no"     (wasted round trip)
                         ──▶ "yes" ──▶ task
```

Problems:
- **Latency floor**: Minimum latency = poll interval. A 1s poll interval adds 0–1s of delay to every task
- **Wasted resources**: Most polls return empty when load is low
- **Thundering herd**: Under load spikes, all workers poll simultaneously
- **Scaling tradeoff**: Faster polling reduces latency but increases load on the queue

### Push Model (chosen)

```
Orchestrator ──stream──▶ Worker (instant delivery)
```

Advantages:
- **Near-zero dispatch latency**: Tasks arrive as soon as they're scheduled
- **No wasted work**: Messages only flow when there are tasks
- **Backpressure built-in**: gRPC flow control handles overload naturally
- **Connection reuse**: One persistent stream per worker, no connection churn

## Streaming Model (gRPC)

Ortrix uses **bidirectional gRPC streaming** between orchestrators and workers:

```protobuf
service WorkerService {
  rpc StreamTasks(stream WorkerMessage) returns (stream OrchestratorMessage);
}
```

### Stream Lifecycle

```
┌────────────┐                          ┌───────────────┐
│   Worker    │                          │ Orchestrator   │
│  (SDK)      │                          │                │
├─────────────┤                          ├────────────────┤
│             │──WorkerRegistration────▶│                │
│             │  {id, capabilities}      │  register      │
│             │                          │  worker        │
│             │◀──Ack──────────────────│                │
│             │                          │                │
│             │──Heartbeat─────────────▶│  track         │
│             │  {timestamp}             │  liveness      │
│             │                          │                │
│             │◀──Task─────────────────│  dispatch      │
│             │  {id, type, payload}     │  task          │
│             │                          │                │
│             │──TaskResult────────────▶│  record        │
│             │  {task_id, output}       │  result        │
│             │                          │                │
│             │──Heartbeat─────────────▶│                │
│             │                          │                │
│             │◀──Task─────────────────│                │
│             │  ...                     │  ...           │
└─────────────┘                          └────────────────┘
```

### Message Types

**Worker → Orchestrator (WorkerMessage)**:

| Message              | Purpose                           |
|----------------------|-----------------------------------|
| WorkerRegistration   | Announce capabilities on connect  |
| TaskResult           | Return task execution result      |
| Heartbeat            | Liveness signal                   |

**Orchestrator → Worker (OrchestratorMessage)**:

| Message   | Purpose                              |
|-----------|--------------------------------------|
| Task      | Push a task for execution            |
| Ack       | Acknowledge receipt of worker msg    |

## Push with Backpressure (Flow-Controlled Push)

Ortrix uses a push-based model, but push does **not** mean uncontrolled push. The orchestrator respects worker-advertised capacity and never dispatches more tasks than a worker can handle. This model is called **flow-controlled push**.

### Why Push ≠ Uncontrolled Push

A naive push model would overwhelm workers:

```
  WRONG: Orchestrator fires tasks blindly
    Orchestrator ──task──▶ Worker (busy)
    Orchestrator ──task──▶ Worker (busy)
    Orchestrator ──task──▶ Worker (overloaded!)
```

Ortrix avoids this by requiring workers to **advertise available capacity**. The orchestrator only pushes tasks when the worker has declared it can accept them.

### Capacity Advertisement

Workers send a capacity signal as part of their registration and on every status update:

```
  Worker → Orchestrator:  READY { max_concurrent: 10, available_slots: 7 }
```

The orchestrator maintains a real-time view of each worker's available capacity:

```
  ┌──────────────────────────────────────────────────┐
  │  Worker Capacity Table (Orchestrator)            │
  │                                                  │
  │  Worker        Max    In-Flight    Available     │
  │  svc-a-1       10     3            7             │
  │  svc-a-2       10     10           0  (full)     │
  │  svc-b-1       20     5            15            │
  └──────────────────────────────────────────────────┘
```

### Flow Control Protocol

```
  ┌─────────────┐                          ┌───────────────┐
  │   Worker     │                          │ Orchestrator   │
  ├─────────────┤                          ├────────────────┤
  │             │──Register + READY(10)──▶│  records       │
  │             │  {capacity: 10}          │  capacity=10   │
  │             │                          │                │
  │             │◀──Task─────────────────│  capacity=9    │
  │             │◀──Task─────────────────│  capacity=8    │
  │             │◀──Task─────────────────│  capacity=7    │
  │             │                          │                │
  │             │──TaskResult────────────▶│  capacity=8    │
  │             │  (slot freed)            │                │
  │             │                          │                │
  │             │──READY(capacity=5)─────▶│  capacity=5    │
  │             │  (explicit update)       │  (updated)     │
  │             │                          │                │
  │             │   (no tasks sent when    │                │
  │             │    capacity = 0)         │                │
  └─────────────┘                          └────────────────┘
```

### Detailed Flow

1. **Worker connects**: Opens a bidirectional gRPC stream to the orchestrator
2. **Worker sends READY**: Includes maximum concurrent capacity (e.g., `max_concurrent: 10`)
3. **Orchestrator records capacity**: Adds worker to the capability index with its available slots
4. **Orchestrator sends tasks**: Only dispatches tasks if `available_slots > 0`
5. **Worker processes task**: Executes the handler and sends back a `TaskResult`
6. **Capacity updated**: On result receipt, orchestrator increments available slots
7. **Explicit capacity update**: Worker can send a new `READY` message at any time to adjust capacity (e.g., under memory pressure)

### Backpressure Guarantees

| Guarantee                       | How                                           |
|---------------------------------|-----------------------------------------------|
| No overloading                  | Orchestrator tracks per-worker available slots |
| Worker controls concurrency     | Worker sets `max_concurrent` at registration   |
| Dynamic adjustment              | Worker sends updated `READY` messages anytime  |
| Queue buffering                 | Tasks wait in priority queue when all workers are full |
| No task drops                   | Tasks are never dropped — they wait for capacity |
| gRPC-level flow control         | Underlying HTTP/2 flow control provides transport-level backpressure |

## Task Lifecycle

A task progresses through well-defined states:

```
  PENDING ──▶ SCHEDULED ──▶ DISPATCHED ──▶ RUNNING ──▶ COMPLETED
                                              │
                                              ├──▶ FAILED ──▶ RETRY ──▶ SCHEDULED
                                              │
                                              └──▶ COMPENSATING ──▶ COMPENSATED
```

| State         | Description                                           |
|---------------|-------------------------------------------------------|
| PENDING       | Task accepted, written to WAL                         |
| SCHEDULED     | Task enqueued in priority scheduler                   |
| DISPATCHED    | Task pushed to a worker via stream                    |
| RUNNING       | Worker acknowledged receipt, executing                |
| COMPLETED     | Worker returned success                               |
| FAILED        | Worker returned error or timed out                    |
| RETRY         | Task re-enqueued after failure (within retry limit)   |
| COMPENSATING  | Saga compensation in progress                         |
| COMPENSATED   | Compensation completed                                |

### State Transitions and WAL

Every state transition is recorded in the WAL before it takes effect:

```
1. TaskCreated      → PENDING
2. TaskScheduled    → SCHEDULED
3. TaskDispatched   → DISPATCHED (includes worker_id)
4. TaskRunning      → RUNNING
5. TaskCompleted    → COMPLETED (includes output)
6. TaskFailed       → FAILED (includes error)
7. TaskRetrying     → RETRY → re-enters SCHEDULED
8. TaskCompensating → COMPENSATING
9. TaskCompensated  → COMPENSATED
```

## Payload Structure

### Task

```protobuf
message Task {
  string id       = 1;  // Unique task identifier (UUID)
  string type     = 2;  // Task type (maps to capability)
  bytes  payload  = 3;  // Serialized input data
  map<string, string> metadata = 4;  // Key-value metadata
  int32  priority = 5;  // 0=LOW, 1=MEDIUM, 2=HIGH
  int64  created_at = 6;  // Unix timestamp (nanos)
}
```

### TaskResult

```protobuf
message TaskResult {
  string task_id     = 1;  // References the original task
  bool   success     = 2;  // Execution outcome
  bytes  output      = 3;  // Serialized result data
  string error       = 4;  // Error message if failed
  int64  completed_at = 5; // Unix timestamp (nanos)
}
```

### Design Decisions

- **`bytes` for payload/output**: Schema-agnostic. Services choose their own serialization (JSON, Protobuf, Avro)
- **`metadata` map**: Extensible key-value pairs for routing hints, trace IDs, deadlines
- **`priority` as int32**: Allows future expansion beyond 3 levels without breaking the wire format
- **Large payloads**: When payload exceeds a configured threshold, only a **reference** (e.g., S3 URI) is stored in the task. The actual data is stored externally. See [State and WAL](state-and-wal.md) for details.
