# Proposal: Partition Rebalancing

## Table of Contents

- [Status](#status)
- [Problem Statement](#problem-statement)
- [Motivation](#motivation)
- [Design Details](#design-details)
  - [Locality-Based Migration](#locality-based-migration)
  - [Load-Based Rebalancing](#load-based-rebalancing)
  - [Partition Movement Flow](#partition-movement-flow)
- [Alternatives Considered](#alternatives-considered)
- [Tradeoffs](#tradeoffs)
- [Testing Strategy](#testing-strategy)
- [Rollout Plan](#rollout-plan)

---

## Status

**Proposed**

---

## Problem Statement

Ortrix assigns partitions to orchestrators using hash-based distribution. This approach distributes workflows uniformly by key but not by load or locality. A partition may be owned by an orchestrator in a different availability zone from the workers that execute most of its tasks, introducing unnecessary cross-zone latency and egress costs. Similarly, a single high-throughput workflow can saturate one orchestrator while others remain idle.

---

## Motivation

- **Latency reduction**: Cross-zone task dispatch adds 1–5ms of network latency per hop. Moving partitions closer to their execution zones reduces dispatch latency.
- **Cost reduction**: Cloud providers charge for cross-zone and cross-region data transfer. Locality-aligned partitions minimize egress costs.
- **Load fairness**: Without load-based rebalancing, hot orchestrators degrade dispatch latency for all their partitions, not just the hot one.
- **Operational health**: Balanced clusters are easier to reason about, monitor, and capacity-plan.

---

## Design Details

### Locality-Based Migration

Track execution locality metrics per partition — record which zone, node, and pod most frequently execute tasks for a given partition. Use these signals to migrate partitions closer to their execution hotspot.

**Metrics collected per partition:**

| Metric                    | Description                                        |
|---------------------------|----------------------------------------------------|
| `execution_zone`          | Most frequent zone for task completions            |
| `owner_zone`              | Current owner zone                                 |
| `cross_zone_ratio`        | Fraction of tasks dispatched cross-zone            |
| `migration_cost`          | Estimated state transfer time                      |

**Migration decision criteria:**

```
if cross_zone_ratio > threshold (e.g., 0.5)
   AND migration_cost < max_allowed_downtime
   AND cooldown_elapsed:
     trigger migration to execution_zone
```

### Load-Based Rebalancing

Continuously monitor per-partition and per-node resource metrics. When imbalance exceeds a threshold, trigger partition moves from hot nodes to cold nodes.

**Hot node detection:**

```
score = w₁ × cpu_usage + w₂ × queue_depth + w₃ × p99_dispatch_latency

if score > hot_threshold:
    mark node as HOT
```

**Rebalancing strategy:**

1. **Detect**: Identify hot nodes using weighted scoring
2. **Select**: Pick the partition with the highest marginal load on the hot node
3. **Target**: Choose the coldest node with sufficient capacity
4. **Migrate**: Execute partition migration (see movement flow below)
5. **Cooldown**: Suppress further moves for a cooldown period to prevent oscillation

**Safeguards:**

- Minimum interval of 60s between moves
- Exponential backoff on repeated moves of the same partition
- Dry-run mode for operator review before execution

### Partition Movement Flow

```
Phase 1: PREPARE
┌─────────────────┐         ┌─────────────────┐
│  Source Owner    │────────▶│  Target Owner    │
│  (Partition P)   │  state  │  (Standby)       │
└─────────────────┘  xfer   └─────────────────┘

Phase 2: FENCE
  Source stops accepting new tasks for P
  Flushes in-flight WAL entries
  Transfers final state delta to target

Phase 3: ACTIVATE
  Target acquires lease for P (new epoch)
  Source releases lease
  Gateway updates routing metadata

Phase 4: RESUME
  Target begins accepting tasks for P
  Workers reconnect to new owner
```

**State transfer options:**

| Approach       | Downtime     | Complexity | Data Safety       |
|---------------|-------------|------------|-------------------|
| Stop-and-copy  | Seconds      | Low        | High (clean cut)  |
| Live migration | Milliseconds | High       | Needs fencing     |
| Dual-write     | Zero         | Very high  | Risk of conflicts |

Initial implementation uses stop-and-copy with pre-warming to minimize complexity.

---

## Alternatives Considered

| Alternative                | Pros                          | Cons                                 | Why Not                              |
|---------------------------|-------------------------------|--------------------------------------|--------------------------------------|
| Static manual assignment   | Simple, predictable           | No adaptation to runtime conditions  | Requires operator intervention       |
| Consistent hashing rings   | Minimal data movement         | Doesn't address load imbalance       | Locality still not considered        |
| Full state replication      | Zero-downtime migration       | High resource cost, complexity       | Overkill for initial implementation  |

---

## Tradeoffs

- **Availability vs optimality**: Partition migration causes brief unavailability for the moved partition. The benefit is better steady-state latency and cost.
- **Stability vs responsiveness**: Aggressive rebalancing can cause oscillation. Cooldown periods trade responsiveness for stability.
- **Simplicity vs performance**: Stop-and-copy is simpler but causes seconds of downtime. Live migration is faster but significantly more complex.
- **Resource overhead**: Locality tracking adds per-partition metric collection overhead.

---

## Testing Strategy

- **Unit tests**: Test migration decision logic (threshold evaluation, cooldown, scoring)
- **Integration tests**: Simulate partition migration between two orchestrator instances, verify state consistency after migration
- **Failure tests**: Crash source during migration, crash target during migration, network partition during state transfer
- **Load tests**: Measure rebalancing effectiveness under skewed workloads
- **Correctness tests**: Verify no tasks are lost or duplicated during migration

---

## Rollout Plan

1. **Phase 1**: Implement partition movement flow (stop-and-copy) with operator-triggered migration only. Validate correctness and measure downtime.
2. **Phase 2**: Add locality metrics collection and automated locality-based migration behind a feature flag. Run in dry-run mode for observation.
3. **Phase 3**: Add load-based rebalancing with configurable thresholds. Enable automated rebalancing with safeguards (cooldown, dry-run).
