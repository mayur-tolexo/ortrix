# Ortrix Documentation


## Table of Contents

- [Design Documents](#design-documents)
- [Proposals](#proposals)
- [Roadmap](#roadmap)
- [Testing](#testing)

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
| [Future Work](future-work.md) | Research directions and planned improvements |

## Proposals

Design proposals for upcoming features. Each proposal follows the [template](proposals/template.md).

| Proposal | Description |
|----------|-------------|
| [Partition Rebalancing](proposals/partition-rebalancing.md) | Locality-based migration, load-based rebalancing, partition movement flow |
| [Streaming Protocol](proposals/streaming-protocol.md) | gRPC bidirectional streaming, connection lifecycle, backpressure handling |
| [Worker SDK](proposals/worker-sdk.md) | SDK design, handler registration, retry and rate limiting |
| [WAL and Recovery](proposals/wal-and-recovery.md) | WAL format, snapshot strategy, recovery flow |
| [Scheduling Engine](proposals/scheduling-engine.md) | Priority queues, fairness, locality-aware routing |
| [Gateway Control Plane](proposals/gateway-control-plane.md) | Bootstrap flow, auth (mTLS), metadata routing |

## Roadmap

See [roadmap.md](roadmap.md) for the phased development plan with goals, deliverables, and dependencies.

## Testing

See [testing.md](testing.md) for the testing strategy, coverage requirements, and CI enforcement policies.
