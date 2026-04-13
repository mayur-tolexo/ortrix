# Comparison: Flowd vs Temporal

## Overview

| Dimension          | Flowd                                  | Temporal                               |
|--------------------|----------------------------------------|----------------------------------------|
| **Model**          | Partitioned task orchestrator          | Workflow-as-code engine                |
| **Execution**      | Push-based, gRPC streaming             | Pull-based, long polling               |
| **State**          | Event-sourced WAL, in-memory           | Event-sourced, database-backed         |
| **Workers**        | Embedded SDK, no separate services     | Standalone worker processes            |
| **Routing**        | Capability-based, locality-aware       | Task queue based                       |
| **Gateway**        | Control plane only                     | Frontend service in execution path     |
| **Target**         | Low-latency, Kubernetes-native         | General-purpose workflow engine         |

## Latency

### Task Dispatch Latency

```
  Temporal (long polling):
    Worker ──poll──▶ Frontend ──▶ Matching ──▶ History
    Average: poll_interval / 2 = ~500ms (default 1s poll)
    Best case: ~200ms (network RTT + service hops)

  Flowd (push):
    Orchestrator ──stream──▶ Worker
    Average: ~1-2ms (direct stream, single hop)
    Best case: ~500μs (same-node)
```

| Metric              | Temporal        | Flowd          | Improvement |
|--------------------|-----------------|----------------|-------------|
| P50 dispatch       | ~500ms          | ~1ms           | ~500x       |
| P99 dispatch       | ~1000ms         | ~5ms           | ~200x       |
| End-to-end (simple)| ~600ms          | ~3ms           | ~200x       |

Flowd's push model eliminates the polling latency floor that is fundamental to Temporal's architecture.

### Why the Difference Matters

For **high-throughput, low-latency** workloads:
- Real-time event processing
- Synchronous API orchestration
- Interactive user-facing workflows
- High-frequency trading operations

The 500ms polling floor in Temporal makes it unsuitable for sub-10ms dispatch requirements.

For **long-running workflows** (hours/days):
- The 500ms dispatch latency is negligible
- Temporal's rich workflow semantics (timers, signals, queries) are more valuable
- Flowd's latency advantage is less relevant

## Architecture Differences

### Component Topology

```
  Temporal Architecture:
  ┌─────────┐   ┌──────────┐   ┌──────────┐   ┌──────────┐
  │ Client   │──▶│ Frontend │──▶│ Matching  │──▶│ History  │
  └─────────┘   └──────────┘   └──────────┘   └──────────┘
                                                    │
                                               ┌────▼────┐
                                               │Database │
                                               │(Cassandra│
                                               │/Postgres)│
                                               └─────────┘
                     ▲
                     │ long poll
                ┌────┴─────┐
                │  Worker   │  (separate process)
                └──────────┘

  Flowd Architecture:
  ┌─────────┐   ┌──────────┐
  │ Client   │──▶│ Gateway  │  (control plane only)
  └─────────┘   └──────────┘
                      │ routing metadata
                      ▼
                ┌──────────────┐
                │ Orchestrator  │  (partitioned, in-memory + WAL)
                └──────┬───────┘
                       │ gRPC stream (push)
                ┌──────▼───────┐
                │ Service       │  (embedded worker SDK)
                └──────────────┘
```

### Key Architectural Differences

| Aspect               | Temporal                              | Flowd                                   |
|----------------------|---------------------------------------|-----------------------------------------|
| **Service hops**     | Client → Frontend → Matching → History| Client → Gateway → Orchestrator → Worker|
| **Data plane hops**  | 3 (Frontend, Matching, History)       | 1 (Orchestrator → Worker direct)        |
| **State storage**    | External database                     | In-memory + WAL (no external DB)        |
| **Worker model**     | Separate long-polling processes       | Embedded SDK in existing services       |
| **Partition model**  | Shard by namespace + workflow ID      | hash(workflow_id) → partition            |
| **Scaling unit**     | Service-level scaling                 | Partition-level scaling                  |

### Database Dependency

**Temporal** requires an external database (Cassandra, PostgreSQL, MySQL):
- All state is persisted to the database on every transition
- Database becomes the throughput bottleneck
- Requires database expertise to operate at scale
- Database latency directly impacts workflow latency

**Flowd** uses no external database:
- State is kept in-memory for reads
- WAL provides durability (local + replicated)
- Snapshots enable fast recovery
- Eliminates database as bottleneck and operational dependency

### Worker Infrastructure

**Temporal** workers are standalone processes:
- Require separate deployment and scaling
- Long-poll the Temporal server for tasks
- Each task type needs a dedicated worker fleet
- Worker failures don't affect the service they work for

**Flowd** workers are embedded in your services:
- No separate worker infrastructure to manage
- Worker SDK runs inside your existing pods
- Service declares capabilities, receives tasks on gRPC stream
- Fewer moving parts, simpler operational model

## Tradeoffs

### Where Flowd Wins

| Area                    | Advantage                                       |
|------------------------|------------------------------------------------|
| Dispatch latency       | ~1ms vs ~500ms — push vs poll                  |
| Operational simplicity | No external DB, embedded workers               |
| Resource efficiency    | No idle polling, no separate worker processes   |
| Kubernetes-native      | Built for K8s from day one                     |
| Scaling                | Partition-based, fine-grained                  |
| Data locality          | Locality-aware scheduling                      |

### Where Temporal Wins

| Area                    | Advantage                                       |
|------------------------|------------------------------------------------|
| Workflow semantics     | Timers, signals, queries, child workflows      |
| Maturity               | Production-proven at massive scale              |
| Language support        | Go, Java, Python, TypeScript, PHP, .NET        |
| Ecosystem              | Rich tooling, UI, visibility                   |
| Long-running workflows | Better primitives for multi-day workflows      |
| Community              | Large open-source community, Temporal Cloud    |

### When to Choose Flowd

- You need **sub-10ms task dispatch latency**
- You're running on **Kubernetes** and want native integration
- You want **embedded workers** without separate infrastructure
- Your workload is **high-throughput, short-duration tasks**
- You prefer **operational simplicity** (no external database)
- You need **locality-aware scheduling** for data-intensive tasks

### When to Choose Temporal

- You need **rich workflow semantics** (timers, signals, child workflows)
- You have **long-running workflows** (hours to days)
- You need **multi-language support** beyond Go
- You want a **battle-tested** system with a large community
- You need **Temporal Cloud** managed service
- Dispatch latency of ~500ms is acceptable for your use case

## Summary

Flowd and Temporal solve overlapping but distinct problems. Temporal is a **general-purpose workflow engine** optimized for correctness and rich workflow semantics. Flowd is a **high-performance task orchestrator** optimized for low latency and Kubernetes-native operation.

```
  Latency Spectrum:

  1ms        10ms       100ms      500ms      1s         10s
  ├──────────┤──────────┤──────────┤──────────┤──────────┤
  │◀─ Flowd ─▶│                    │◀─ Temporal ──────────▶│
  │  sweet spot                    │   sweet spot          │
```

Choose based on your primary constraint: if it's **latency and operational simplicity**, choose Flowd. If it's **workflow richness and ecosystem maturity**, choose Temporal.
