# Ortrix Documentation


## Table of Contents

- [Design Documents](#design-documents)

---

## Design Documents

| Document | Description |
|----------|-------------|
| [Architecture](architecture.md) | High-level system design, control plane vs data plane, component roles |
| [Execution Model](execution-model.md) | Push-based dispatch, gRPC streaming, task lifecycle |
| [State and WAL](state-and-wal.md) | Event sourcing, WAL structure, snapshots, recovery |
| [Partitioning and Scaling](partitioning-and-scaling.md) | Partition ownership, leases, rebalancing, horizontal scaling |
| [Scheduling and Routing](scheduling-and-routing.md) | Capability-based routing, locality-aware scheduling, priority queues |
| [Failure Handling](failure-handling.md) | Crash recovery, idempotency, saga compensation |
| [Security](security.md) | mTLS, service identity, authorization |
| [Performance](performance.md) | Latency analysis, batching, WAL optimization |
| [Comparison](comparison.md) | Ortrix vs Temporal — architecture, latency, tradeoffs |
