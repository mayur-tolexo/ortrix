# Performance

## Latency Analysis: Push vs Pull

The choice of push-based execution is the single largest performance decision in Flowd's architecture.

### Pull Model Latency

```
  Poll Interval: P (e.g., 1 second)
  Average Queue Wait: P/2

  Task Ready ──▶ wait P/2 ──▶ Worker Polls ──▶ Execute
                   │
                   └── 500ms average added latency (P=1s)
```

| Poll Interval | Avg Added Latency | Polls/sec (1000 workers) |
|--------------|-------------------|--------------------------|
| 100ms        | 50ms              | 10,000                   |
| 500ms        | 250ms             | 2,000                    |
| 1s           | 500ms             | 1,000                    |
| 5s           | 2,500ms           | 200                      |

Reducing poll interval improves latency but increases load on the queue proportionally.

### Push Model Latency

```
  Task Ready ──▶ Orchestrator Sends ──▶ Worker Receives ──▶ Execute
                        │
                        └── ~1ms network hop (same zone)
```

| Component            | Latency        |
|---------------------|----------------|
| Scheduler selection | ~10μs          |
| gRPC send           | ~50μs          |
| Network (same zone) | ~500μs–1ms    |
| **Total dispatch**  | **~1–2ms**     |

Push eliminates the polling floor entirely. Dispatch latency is bounded by network RTT, not poll interval.

### End-to-End Comparison

| Metric                  | Pull (1s poll) | Push (Flowd) |
|------------------------|----------------|--------------|
| Dispatch latency       | 500ms avg      | ~1ms         |
| P99 dispatch latency   | ~1000ms        | ~5ms         |
| Idle overhead          | Continuous     | Zero         |
| Burst response         | Up to 1s delay | Immediate    |

## Batching

Flowd uses batching at multiple levels to amortize overhead.

### WAL Write Batching

Individual WAL writes are expensive due to `fsync`. Flowd batches WAL entries within a configurable window:

```
  ┌───────────────────────────────────────┐
  │         WAL Write Batcher             │
  │                                       │
  │  Batch Window: 1ms                    │
  │  Max Batch Size: 100 entries          │
  │                                       │
  │  entry-1 ─┐                           │
  │  entry-2 ─┤                           │
  │  entry-3 ─┼──▶ single fsync ──▶ disk │
  │  entry-4 ─┤                           │
  │  entry-5 ─┘                           │
  └───────────────────────────────────────┘
```

| Batching         | fsyncs/sec (10K events/sec) | Latency Added |
|-----------------|----------------------------|---------------|
| No batching     | 10,000                     | 0             |
| 1ms window      | ~1,000                     | ≤1ms          |
| 5ms window      | ~200                       | ≤5ms          |

**Tradeoff**: Batching adds up to `batch_window` latency but dramatically reduces disk I/O.

### gRPC Message Batching

When dispatching multiple tasks to the same worker, the orchestrator can batch them into a single gRPC send:

```
  Without batching: send(task1), send(task2), send(task3)  → 3 syscalls
  With batching:    send([task1, task2, task3])             → 1 syscall
```

### Snapshot Batching

Snapshots are taken after N events rather than on every state change:

```
  Event 1    → update state
  Event 2    → update state
  ...
  Event 10K  → update state + take snapshot
```

## WAL Optimization

### Memory-Mapped I/O

The local WAL uses memory-mapped files for writes:

```
  Application ──write──▶ mmap'd region ──▶ OS page cache ──▶ disk
                              │
                         zero-copy (no user/kernel boundary crossing)
```

Benefits:
- Write path avoids `write()` syscall overhead
- OS manages page cache efficiently
- Sequential writes leverage disk prefetching

### Segment-Based WAL

The WAL is split into fixed-size segments:

```
  wal/
  ├── segment-000001.wal  (64MB, sealed)
  ├── segment-000002.wal  (64MB, sealed)
  ├── segment-000003.wal  (64MB, sealed)
  └── segment-000004.wal  (12MB, active)
```

| Property        | Value  | Reason                                    |
|----------------|--------|-------------------------------------------|
| Segment size   | 64MB   | Balance between file count and reclaim speed |
| Pre-allocation | Yes    | Avoid filesystem metadata updates on append  |
| Reclaim        | After snapshot | Delete segments fully covered by snapshot  |

### Compression

WAL entries are compressed before writing:

| Codec    | Ratio | Speed      | Use Case              |
|----------|-------|------------|-----------------------|
| LZ4      | ~2x   | ~4 GB/s    | Default (speed focus) |
| Zstd     | ~3x   | ~1 GB/s    | High compression mode |
| None     | 1x    | Max        | Ultra-low latency     |

Default is **LZ4**: fast enough to not appear on profiles, reduces WAL disk usage by ~50%.

## Connection Reuse

### Persistent gRPC Streams

Each worker maintains **one persistent bidirectional stream** to its orchestrator:

```
  Without connection reuse:
    task1: dial → TLS handshake → HTTP/2 → send → close  (50ms overhead)
    task2: dial → TLS handshake → HTTP/2 → send → close  (50ms overhead)

  With persistent stream:
    connect once: dial → TLS handshake → HTTP/2 stream open  (50ms, once)
    task1: send on existing stream  (~50μs)
    task2: send on existing stream  (~50μs)
    task3: send on existing stream  (~50μs)
    ...
```

### Connection Pool (Gateway → Orchestrator)

The gateway maintains a connection pool to orchestrator instances:

```
  ┌──────────┐     pool[0] ──▶ orchestrator-0
  │ Gateway   │     pool[1] ──▶ orchestrator-1
  │           │     pool[2] ──▶ orchestrator-2
  └──────────┘

  Pool size per orchestrator: configurable (default: 4)
  Connection lifetime: 1 hour (re-establish to pick up new TLS certs)
  Health check: gRPC keepalive pings every 30s
```

### HTTP/2 Multiplexing

gRPC uses HTTP/2, which multiplexes multiple streams over a single TCP connection:

```
  Single TCP Connection:
    ├── Stream 1: Worker A task dispatch
    ├── Stream 2: Worker A result
    ├── Stream 3: Worker A heartbeat
    └── (all multiplexed, no head-of-line blocking at HTTP level)
```

## Payload Optimization

### Small Payloads: Inline

For payloads under the threshold (default 64KB), data is embedded directly in the protobuf message:

```
  Task{payload: <actual bytes>}  → single message, no extra I/O
```

### Large Payloads: External Reference

For payloads over the threshold, only a reference is transmitted:

```
  Task{payload: <reference URI>}  → small message
  Worker: fetch actual data from object store (parallel, resumable)
```

### Zero-Copy Where Possible

- Protobuf `bytes` fields avoid unnecessary copies in the gRPC stack
- Worker SDK passes payload slices directly to handlers without copying
- WAL writes use memory-mapped I/O to avoid user-space buffers

### Metadata Overhead

The protobuf wire format adds minimal overhead:

| Payload Size | Wire Overhead | Overhead % |
|-------------|---------------|------------|
| 100 bytes   | ~20 bytes     | 20%        |
| 1 KB        | ~20 bytes     | 2%         |
| 10 KB       | ~20 bytes     | 0.2%       |
| 64 KB       | ~20 bytes     | 0.03%      |

## Performance Tuning Parameters

| Parameter               | Default   | Description                              |
|------------------------|-----------|------------------------------------------|
| `wal.batch_window`     | 1ms       | WAL write batch window                   |
| `wal.segment_size`     | 64MB      | WAL segment file size                    |
| `wal.compression`      | lz4       | WAL compression codec                    |
| `scheduler.dispatch_batch` | 10    | Max tasks per dispatch batch             |
| `snapshot.interval`    | 10000     | Events between snapshots                 |
| `grpc.max_msg_size`    | 4MB       | Max gRPC message size                    |
| `grpc.keepalive`       | 30s       | Keepalive ping interval                  |
| `payload.threshold`    | 64KB      | Inline vs external payload threshold     |
| `worker.max_concurrent`| 100       | Max concurrent tasks per worker          |
