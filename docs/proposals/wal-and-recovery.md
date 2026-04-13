# Proposal: WAL and Recovery

## Table of Contents

- [Status](#status)
- [Problem Statement](#problem-statement)
- [Motivation](#motivation)
- [Design Details](#design-details)
  - [WAL Format](#wal-format)
  - [Snapshot Strategy](#snapshot-strategy)
  - [Recovery Flow](#recovery-flow)
- [Alternatives Considered](#alternatives-considered)
- [Tradeoffs](#tradeoffs)
- [Testing Strategy](#testing-strategy)
- [Rollout Plan](#rollout-plan)

---

## Status

**Proposed**

---

## Problem Statement

Ortrix uses an event-sourced write-ahead log (WAL) for durability and state recovery. The WAL format, snapshot strategy, and recovery flow must be carefully designed to balance write throughput, storage efficiency, recovery speed, and data safety. This proposal defines the detailed specification for each of these components.

---

## Motivation

- **Durability**: The WAL is the single source of truth for task state. Any corruption or data loss in the WAL means lost work.
- **Recovery speed**: On orchestrator crash or partition migration, recovery time is dominated by WAL replay. Faster recovery means less downtime.
- **Storage efficiency**: Unbounded WAL growth consumes disk and slows recovery. Snapshots and compaction keep storage bounded.
- **Operational clarity**: A well-defined WAL format enables debugging tools (WAL inspection, replay, corruption detection).

---

## Design Details

### WAL Format

Each WAL entry is a fixed-header, variable-body binary record:

```
┌────────┬────────┬──────────┬───────────┬──────────┬──────────┬──────────┐
│ Length  │ CRC32  │ Sequence │ EventType │ TaskID   │ Data     │ Epoch    │
│ (4B)   │ (4B)   │ (8B)     │ (2B)      │ (var)    │ (var)    │ (8B)     │
└────────┴────────┴──────────┴───────────┴──────────┴──────────┴──────────┘
```

**Field descriptions:**

| Field      | Size     | Description                                        |
|-----------|----------|----------------------------------------------------|
| Length     | 4 bytes  | Total entry length (excluding this field)          |
| CRC32     | 4 bytes  | Checksum over remaining fields for integrity       |
| Sequence   | 8 bytes  | Monotonically increasing sequence number           |
| EventType  | 2 bytes  | Event type enum (TaskCreated, TaskCompleted, etc.) |
| TaskID     | Variable | Length-prefixed task identifier string              |
| Data       | Variable | Serialized event payload (Protobuf)                |
| Epoch      | 8 bytes  | Partition ownership epoch (fencing token)          |

**Event types:**

| Code | Event              | Data Contents                            |
|------|--------------------|------------------------------------------|
| 0x01 | TaskCreated        | Task type, payload ref, priority, meta   |
| 0x02 | TaskScheduled      | Partition ID                             |
| 0x03 | TaskDispatched     | Worker ID                                |
| 0x04 | TaskCompleted      | Output ref (or inline if small)          |
| 0x05 | TaskFailed         | Error message, retry count               |
| 0x06 | TaskRetrying       | Next retry timestamp                     |
| 0x07 | TaskCompensating   | Compensation task ID                     |
| 0x08 | TaskCompensated    | Compensation result                      |
| 0x09 | TaskCancelled      | Cancellation reason                      |

**Segment-based storage:**

```
wal/
├── segment-000001.wal  (64MB, sealed)
├── segment-000002.wal  (64MB, sealed)
├── segment-000003.wal  (64MB, sealed)
└── segment-000004.wal  (12MB, active)
```

- Segments are pre-allocated at 64MB to avoid filesystem metadata updates on append
- Only the active segment accepts writes
- Sealed segments are immutable and eligible for replication and cleanup
- Segments are compressed with LZ4 by default (configurable: LZ4, Zstd, None)

**Write path:**

```
1. Serialize event to bytes
2. Compute CRC32 over serialized bytes
3. Append to active segment (memory-mapped I/O)
4. Batch fsync within configurable window (default: 1ms)
5. Update in-memory state
6. Acknowledge to caller
7. [async] Replicate to remote WAL
```

### Snapshot Strategy

Snapshots capture the full in-memory state at a point in time, enabling recovery to skip replaying the entire WAL.

**Snapshot contents:**

```
┌──────────────────────────────────┐
│           Snapshot                │
│                                  │
│  Format Version: 1               │
│  WAL Sequence: 150432            │
│  Partition ID: 7                 │
│  Timestamp: 2025-01-15T10:30:00Z │
│                                  │
│  Tasks:                          │
│    task-abc → {state, worker, …} │
│    task-def → {state, worker, …} │
│                                  │
│  Checksum: sha256:a1b2c3…        │
└──────────────────────────────────┘
```

**Snapshot triggers:**

| Trigger             | Condition                            | Description                          |
|--------------------|--------------------------------------|--------------------------------------|
| Periodic            | Every N events (default: 10,000)     | Bounds replay length on recovery     |
| Graceful shutdown   | Orchestrator stopping                | Minimizes next startup time          |
| On demand           | Operator or rebalancing trigger      | Supports partition migration         |
| Size-based          | WAL exceeds N MB since last snapshot | Prevents unbounded WAL growth        |

**Snapshot lifecycle:**

```
1. Acquire read lock on in-memory state (or COW snapshot)
2. Serialize state to snapshot file
3. Record WAL sequence number in snapshot header
4. Release lock
5. Write snapshot to disk with checksum
6. Truncate WAL entries before snapshot sequence
7. [async] Replicate snapshot to remote storage
```

The pause window is minimized using copy-on-write semantics — the snapshot serializes from an immutable view while writes continue against the current state.

### Recovery Flow

```
┌─────────────┐     ┌────────────────┐     ┌─────────────┐
│ Load Latest  │────▶│ Replay WAL     │────▶│ Ready to    │
│ Snapshot     │     │ from snapshot  │     │ Serve       │
│              │     │ sequence       │     │             │
└─────────────┘     └────────────────┘     └─────────────┘
```

**Recovery steps:**

1. Find latest snapshot for this partition (local first, then remote)
2. Validate snapshot checksum
3. Deserialize snapshot → in-memory state
4. Find WAL entries after snapshot sequence number
5. Validate each WAL entry CRC32 before replay
6. Replay each WAL entry to update in-memory state
7. Verify epoch — reject entries with stale epochs
8. Mark partition as ACTIVE
9. Resume accepting new tasks

**Recovery time factors:**

| Factor                        | Impact                            |
|-------------------------------|-----------------------------------|
| Snapshot freshness             | Fewer entries to replay           |
| WAL entry count since snapshot | Linear replay time                |
| Event complexity               | Minimal (state transitions only)  |
| Typical recovery               | Hundreds of ms for 10K events     |

**Corruption handling:**

- CRC32 mismatch on WAL entry → skip entry, log warning, continue replay
- Truncated segment → replay up to last valid entry
- Snapshot corruption → fall back to previous snapshot (or full WAL replay)
- All corruption events are reported via metrics and alerts

---

## Alternatives Considered

| Alternative              | Pros                           | Cons                                     | Why Not                                   |
|-------------------------|-------------------------------|------------------------------------------|------------------------------------------|
| External database (Postgres) | Proven durability, SQL queries | Latency overhead, operational dependency | Contradicts no-external-DB design goal    |
| LSM-tree (RocksDB)       | Efficient reads and writes     | Complex compaction, C++ dependency       | WAL is append-only, simpler model suffices |
| In-memory only           | Fastest possible               | No durability                            | Unacceptable for production use           |

---

## Tradeoffs

- **Write latency vs durability**: Batching fsyncs reduces write latency but introduces a window where recent writes could be lost on crash. Configurable batch window allows users to choose their tradeoff.
- **Snapshot frequency vs recovery time**: Frequent snapshots reduce recovery time but increase I/O during normal operation.
- **Compression vs CPU**: LZ4 compression halves WAL size with minimal CPU cost, but ultra-low-latency deployments may prefer no compression.
- **Local WAL vs replicated WAL**: Local WAL is fast but single-node durable. Async replication adds cross-node durability at the cost of potential data loss on crash (bounded by replication lag).

---

## Testing Strategy

- **Unit tests**: Test WAL serialization/deserialization, CRC32 validation, segment rotation, snapshot serialization
- **Integration tests**: Full write → crash → recovery cycle, verify state consistency after replay
- **Corruption tests**: Inject bit flips in WAL entries, verify CRC detection and graceful degradation
- **Performance tests**: Measure WAL write throughput at various batch windows, measure recovery time vs WAL size
- **Stress tests**: Sustained high-throughput writes with concurrent snapshots, verify no data loss

---

## Rollout Plan

1. **Phase 1**: Implement basic WAL with segment-based storage, CRC32 integrity, and synchronous local writes. Implement periodic snapshots with recovery flow.
2. **Phase 2**: Add write batching with configurable fsync window. Add LZ4 compression. Optimize memory-mapped I/O for write throughput.
3. **Phase 3**: Add asynchronous remote replication. Add WAL compaction for completed workflows. Add WAL inspection and debugging tools.
