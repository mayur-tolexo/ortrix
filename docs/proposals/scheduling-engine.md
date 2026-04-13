# Proposal: Scheduling Engine

## Table of Contents

- [Status](#status)
- [Problem Statement](#problem-statement)
- [Motivation](#motivation)
- [Design Details](#design-details)
  - [Priority Queues](#priority-queues)
  - [Fairness](#fairness)
  - [Locality-Aware Routing](#locality-aware-routing)
- [Alternatives Considered](#alternatives-considered)
- [Tradeoffs](#tradeoffs)
- [Testing Strategy](#testing-strategy)
- [Rollout Plan](#rollout-plan)

---

## Status

**Proposed**

---

## Problem Statement

Ortrix needs a scheduling engine that dispatches tasks to workers efficiently while respecting priority levels, preventing starvation, and optimizing for data locality. The scheduler must handle high throughput (10K+ tasks/sec per partition) with minimal dispatch latency while providing fairness guarantees across priority levels.

---

## Motivation

- **Latency-sensitive workloads**: HIGH priority tasks (user-facing) must be dispatched within milliseconds, even when the system is under load.
- **Starvation prevention**: Without fairness controls, LOW priority background jobs are indefinitely starved when HIGH priority tasks keep arriving.
- **Cost optimization**: Locality-aware routing reduces cross-zone network hops, lowering cloud egress costs and dispatch latency.
- **SLA compliance**: Deadline-aware scheduling ensures tasks complete within their SLA bounds.

---

## Design Details

### Priority Queues

Tasks are enqueued with one of three priority levels:

| Level   | Value | Use Case                               |
|---------|-------|----------------------------------------|
| HIGH    | 2     | User-facing, latency-sensitive         |
| MEDIUM  | 1     | Standard business logic                |
| LOW     | 0     | Background jobs, batch processing      |

**Queue structure (per partition):**

```
┌─────────────────────────────────────┐
│         Scheduler                    │
│                                      │
│  ┌─────────┐  HIGH    ──▶ dispatch  │
│  │ Queue H │  weight: 6             │
│  └─────────┘                        │
│  ┌─────────┐  MEDIUM  ──▶ dispatch  │
│  │ Queue M │  weight: 3             │
│  └─────────┘                        │
│  ┌─────────┐  LOW     ──▶ dispatch  │
│  │ Queue L │  weight: 1             │
│  └─────────┘                        │
└─────────────────────────────────────┘
```

**Within-priority ordering:**

1. **Deadline first**: Tasks with earlier deadlines are dispatched first
2. **FIFO**: Tasks without deadlines are ordered by arrival time

**Queue limits:**

- Each priority queue has a configurable maximum depth
- When a queue is full, new tasks receive `RESOURCE_EXHAUSTED` response
- Queue depth metrics are exposed for auto-scaling decisions

### Fairness

Pure priority scheduling starves lower-priority tasks when higher-priority tasks keep arriving. Ortrix uses **weighted fair queuing (WFQ)** to prevent starvation:

**Dispatch ratio:**

```
HIGH : MEDIUM : LOW = 6 : 3 : 1
```

In every 10 dispatch cycles:
- 6 tasks dispatched from HIGH queue
- 3 tasks dispatched from MEDIUM queue
- 1 task dispatched from LOW queue

**Empty queue redistribution:**

If a queue is empty during its allocation slot, the slot is redistributed to non-empty queues proportionally:

```
Example: HIGH queue empty
  MEDIUM gets: 3 + (6 × 3/4) = 7.5 → 7 tasks
  LOW gets:    1 + (6 × 1/4) = 2.5 → 3 tasks
```

**Starvation bounds:**

| Priority | Max wait (at full load) | Guarantee                        |
|----------|------------------------|----------------------------------|
| HIGH     | Near-zero              | Always dispatched within 1 cycle |
| MEDIUM   | ~3 dispatch cycles     | At least 30% of capacity         |
| LOW      | ~10 dispatch cycles    | At least 10% of capacity         |

**Future enhancement — deadline urgency boost:**

```
effective_priority = base_priority + urgency_boost(remaining_time)
```

As a task's deadline approaches, its effective priority is boosted, allowing it to be dispatched ahead of other tasks at the same base priority level.

### Locality-Aware Routing

When multiple workers can handle a task, the scheduler prefers workers that are topologically close to the orchestrator.

**Locality tiers (highest to lowest preference):**

```
1. Same Pod     (co-located sidecar)     → score: +4
2. Same Node    (same Kubernetes node)    → score: +3
3. Same Zone    (same availability zone)  → score: +2
4. Any          (any available worker)    → score: +1
```

**Worker selection pipeline:**

```
1. Filter by Capability    → workers that declare the required task type
2. Filter by Health        → remove workers with missed heartbeats
3. Filter by Capacity      → remove workers at max concurrent task limit
4. Score by Locality       → prefer topologically closer workers
5. Score by Load           → prefer workers with fewer in-flight tasks
6. Apply Routing Rules     → canary/blue-green traffic splitting
7. Select Best             → highest combined score wins
```

**Locality metadata source:**

Workers report their topology during registration (extracted from Kubernetes downward API):

```
Worker Metadata:
  worker_id: "svc-a-pod-xyz"
  node: "node-3"
  zone: "us-east-1a"
  pod: "svc-a-pod-xyz"
```

**Fallback behavior:**

- No capable workers in preferred locality → fall back to next tier
- All local workers overloaded → dispatch to remote worker
- Task has explicit routing hints → override locality preference

---

## Alternatives Considered

| Alternative               | Pros                              | Cons                                        | Why Not                                    |
|--------------------------|-----------------------------------|---------------------------------------------|--------------------------------------------|
| Simple FIFO queue         | Trivial implementation            | No priority support, no fairness            | Insufficient for mixed workloads           |
| Strict priority (no WFQ)  | Simplest priority model           | Starves LOW indefinitely                    | Unacceptable for background jobs           |
| External scheduler (K8s)  | Leverage existing infrastructure  | Too coarse-grained, not task-aware          | Need sub-ms scheduling decisions           |
| Random worker selection    | Stateless, no tracking needed     | Ignores locality, load, and capacity        | Wastes network and compute resources       |

---

## Tradeoffs

- **Fairness vs strict priority**: WFQ guarantees minimum throughput for all levels but means HIGH tasks occasionally wait behind MEDIUM/LOW tasks.
- **Locality vs load balance**: Preferring local workers can overload nearby workers while remote workers are idle. Load scoring mitigates this.
- **Scheduling overhead vs optimality**: The multi-step selection pipeline adds microseconds per dispatch but significantly improves worker utilization and latency.
- **Queue depth vs backpressure**: Deep queues absorb bursts but increase tail latency. Shallow queues provide faster backpressure signals.

---

## Testing Strategy

- **Unit tests**: Test priority queue ordering, WFQ dispatch ratios, locality scoring, worker selection pipeline
- **Integration tests**: Submit tasks at all priority levels, verify dispatch ordering and fairness ratios match configuration
- **Starvation tests**: Continuously submit HIGH tasks, verify LOW tasks still receive their minimum allocation
- **Locality tests**: Register workers across multiple zones, verify same-zone preference in dispatch decisions
- **Performance tests**: Measure scheduling overhead per dispatch at 10K tasks/sec, verify sub-100μs scheduler latency
- **Deadline tests**: Submit tasks with deadlines, verify deadline-approaching tasks are boosted

---

## Rollout Plan

1. **Phase 1**: Implement three-level priority queues with FIFO ordering within each level. Basic round-robin worker selection filtered by capability and health.
2. **Phase 2**: Add weighted fair queuing with configurable weights. Add locality-aware scoring using Kubernetes topology metadata.
3. **Phase 3**: Add deadline urgency boosting. Add load-aware worker scoring. Expose scheduling metrics for observability.
