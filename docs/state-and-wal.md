# State Management and WAL

## Event-Sourcing Model

Ortrix uses an **event-sourced** state model. Instead of storing the current state of each task, it stores the sequence of **events** that produced the state. Current state is derived by replaying events.

```
Events (source of truth):
  TaskCreated{id=abc, type=payment}
  TaskScheduled{id=abc, partition=3}
  TaskDispatched{id=abc, worker=svc-1}
  TaskCompleted{id=abc, output=...}

Derived State:
  Task{id=abc, state=COMPLETED, worker=svc-1, output=...}
```

### Why Event-Sourcing

| Benefit               | Description                                          |
|-----------------------|------------------------------------------------------|
| Full audit trail      | Every state change is recorded, never overwritten     |
| Deterministic replay  | State can be reconstructed from events alone         |
| Debugging             | Inspect the exact sequence of events for any task    |
| Compensation          | Saga patterns require knowledge of what happened     |
| Partition recovery    | Replay WAL to rebuild in-memory state after crash    |

## What Goes Into the WAL

The WAL stores **events**, not full state snapshots. Each entry is a minimal record of what changed.

### WAL Entry Structure

```
┌─────────┬──────────┬───────────────┬──────────┬────────────┐
│Sequence │ TaskID   │ EventType     │ Data     │ Timestamp  │
│ (uint64)│ (string) │ (enum)        │ (bytes)  │ (int64)    │
└─────────┴──────────┴───────────────┴──────────┴────────────┘
```

### Event Types Written to WAL

| Event              | Data Contents                            |
|--------------------|------------------------------------------|
| TaskCreated        | task type, payload ref, priority, meta   |
| TaskScheduled      | partition ID                             |
| TaskDispatched     | worker ID                                |
| TaskCompleted      | output ref (or inline if small)          |
| TaskFailed         | error message, retry count               |
| TaskRetrying       | next retry timestamp                     |
| TaskCompensating   | compensation task ID                     |
| TaskCompensated    | compensation result                      |
| TaskCancelled      | cancellation reason                      |

### What Does NOT Go Into the WAL

- Full task payloads (stored externally if large)
- Worker registration state (ephemeral, rebuilt on reconnect)
- Routing tables (derived from partition ownership)
- Metrics and telemetry data

## Hybrid WAL Architecture

Ortrix uses a **hybrid WAL** for balancing speed and durability:

```
  ┌──────────────┐     sync write     ┌──────────────┐
  │ Orchestrator  │──────────────────▶│  Local WAL    │
  │               │                    │  (fast, local)│
  └──────────────┘                    └──────┬───────┘
                                              │
                                        async replicate
                                              │
                                       ┌──────▼───────┐
                                       │  Remote WAL   │
                                       │  (durable,    │
                                       │   replicated) │
                                       └──────────────┘
```

| Layer        | Latency    | Durability       | Description                    |
|-------------|-----------|------------------|--------------------------------|
| Local WAL   | ~μs       | Single node      | Memory-mapped file, fsync      |
| Remote WAL  | ~ms       | Multi-node       | Async replication to replicas  |

The local WAL is the synchronous write path. Every event is written to the local WAL before the operation is acknowledged. Asynchronous replication to remote storage provides cross-node durability.

### Write Path

```
1. Event generated (e.g., TaskCreated)
2. Serialize event to bytes
3. Append to local WAL (fsync)
4. Update in-memory state
5. Acknowledge to caller
6. [async] Replicate to remote WAL
```

## Snapshot Strategy

Replaying the entire WAL from the beginning on every recovery is expensive. Snapshots provide a **checkpoint** from which replay can start.

### Snapshot Contents

A snapshot captures the full in-memory state at a point in time:

```
┌──────────────────────────────────┐
│           Snapshot                │
│                                  │
│  WAL Sequence: 150432            │
│  Timestamp: 2025-01-15T10:30:00Z │
│                                  │
│  Tasks:                          │
│    abc → {state=RUNNING, ...}    │
│    def → {state=PENDING, ...}    │
│    ghi → {state=COMPLETED, ...}  │
│                                  │
│  Partition Assignments:          │
│    P0 → orchestrator-1           │
│    P1 → orchestrator-1           │
│                                  │
└──────────────────────────────────┘
```

### When Snapshots Are Taken

- **Periodic**: Every N events (configurable, default 10,000)
- **On shutdown**: Graceful shutdown triggers a snapshot
- **On demand**: Triggered by operator or rebalancing events

### Snapshot Lifecycle

```
1. Pause incoming WAL writes (brief)
2. Serialize in-memory state
3. Write snapshot to disk with WAL sequence number
4. Resume WAL writes
5. Truncate WAL entries before snapshot sequence
6. [async] Replicate snapshot to remote storage
```

The pause window is minimized by serializing from a consistent view (copy-on-write or read lock).

## Recovery Flow

When an orchestrator instance starts (or takes over a partition), it reconstructs state:

```
┌─────────────┐     ┌────────────────┐     ┌─────────────┐
│ Load Latest  │────▶│ Replay WAL     │────▶│ Ready to    │
│ Snapshot     │     │ from snapshot  │     │ Serve       │
│              │     │ sequence       │     │             │
└─────────────┘     └────────────────┘     └─────────────┘
```

### Recovery Steps

```
1. Find latest snapshot for this partition
2. Deserialize snapshot → in-memory state
3. Find WAL entries after snapshot sequence number
4. Replay each WAL entry to update in-memory state
5. Mark partition as ACTIVE
6. Resume accepting new tasks
```

### Recovery Time

| Factor                        | Impact                            |
|-------------------------------|-----------------------------------|
| Snapshot age                  | Fewer entries to replay           |
| WAL entry count since snap    | Linear replay time                |
| Event complexity              | Minimal (state transitions only)  |
| Typical recovery              | Hundreds of ms for 10K events     |

### Failure During Recovery

If the orchestrator crashes during recovery:
1. Another orchestrator acquires the partition lease
2. Recovery restarts from the same snapshot
3. WAL replay is idempotent — replaying the same events produces the same state

## Handling Large Payloads

Task payloads can be arbitrarily large (file processing, batch data). Storing large payloads directly in the WAL degrades performance and increases replication costs.

### External Storage Strategy

```
  ┌──────────┐                    ┌──────────────────┐
  │  Client   │──large payload──▶│  External Store   │
  │           │                   │  (S3/GCS/MinIO)   │
  │           │◀──reference──────│                    │
  └──────────┘                    └──────────────────┘
       │
       │  submit task with reference
       ▼
  ┌──────────┐
  │  Gateway  │
  └──────────┘
```

### Rules

| Payload Size         | Storage                  | WAL Contains         |
|---------------------|--------------------------|----------------------|
| ≤ threshold (64KB)  | Inline in WAL entry      | Full payload         |
| > threshold          | External object store    | Reference URI only   |

### Reference Format

```json
{
  "store": "s3",
  "bucket": "ortrix-payloads",
  "key": "tasks/abc-123/input",
  "size": 15728640,
  "checksum": "sha256:a1b2c3..."
}
```

### Benefits

- WAL stays compact and fast
- Replication bandwidth is predictable
- Snapshots remain small
- Workers fetch payloads directly from object storage (parallel, resumable)

### Cleanup

External payloads are garbage-collected after task completion based on a configurable retention policy. The WAL event `TaskCompleted` or `TaskCancelled` marks the payload as eligible for cleanup.
