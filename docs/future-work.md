# Ortrix — Future Work

This document outlines planned improvements and research directions for Ortrix. Each section describes the problem, proposed approach, tradeoffs, and design considerations.

---

## 1. Partition Rebalancing (Locality-Aware)

### Problem

Partitions may be assigned to orchestrator instances that are topologically far from the workers that execute their workflows. This introduces unnecessary cross-zone or cross-region network hops, increasing dispatch latency and cloud egress costs.

### Proposed Solution

Track **execution locality metrics** per partition — record which zone, node, and pod most frequently execute tasks for a given partition. Use these signals to migrate partitions closer to their execution hotspot.

#### Migration Flow

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
  Target acquires lease for P
  Source releases lease
  Gateway updates routing metadata

  Phase 4: RESUME
  Target begins accepting tasks for P
  Workers reconnect to new owner
```

#### Tradeoffs

| Approach         | Downtime        | Complexity | Data Safety       |
|-----------------|-----------------|------------|-------------------|
| Stop-and-copy    | Seconds         | Low        | High (clean cut)  |
| Live migration   | Milliseconds    | High       | Needs fencing     |
| Dual-write       | Zero            | Very high  | Risk of conflicts |

**Recommended starting point**: Stop-and-copy with pre-warming. Partitions are fenced, state is transferred, and the new owner activates. The pause window is bounded by snapshot size.

### Metrics to Track

- `partition.execution_zone` — most frequent zone for task completions
- `partition.owner_zone` — current owner zone
- `partition.cross_zone_ratio` — fraction of tasks dispatched cross-zone
- `partition.migration_cost` — estimated state transfer time

---

## 2. Load-Based Rebalancing

### Problem

Hash-based partitioning distributes workflows uniformly by key, but not by load. A partition with one high-throughput workflow can saturate an orchestrator while others sit idle.

### Proposed Solution

Continuously monitor per-partition and per-node resource metrics. When imbalance exceeds a threshold, trigger partition moves.

#### Hot Node Detection

```
  ┌──────────────────────────────────────────────────┐
  │              Load Monitor (per node)              │
  │                                                    │
  │  cpu_usage:        72%  ← above threshold (60%)   │
  │  queue_depth:      1840 ← above threshold (1000)  │
  │  p99_dispatch_ms:  12   ← above threshold (10)    │
  │  partition_count:  12                              │
  │                                                    │
  │  → Node marked HOT                                │
  └──────────────────────────────────────────────────┘
```

#### Rebalancing Strategy

1. **Detect**: Identify hot nodes using weighted scoring: `score = w₁·cpu + w₂·queue_depth + w₃·p99_latency`
2. **Select**: Pick the partition with the highest marginal load on the hot node
3. **Target**: Choose the coldest node with sufficient capacity
4. **Migrate**: Execute partition migration (see Section 1)
5. **Cooldown**: Suppress further moves for a cooldown period to prevent oscillation

#### Safeguards

- **Minimum interval**: No partition moves within 60s of the last move
- **Dampening**: Exponential backoff on repeated moves of the same partition
- **Dry-run mode**: Log proposed moves without executing, for operator review

---

## 3. Hot Partition Mitigation

### Problem

A single workflow — or a small set of correlated workflows — hashing to the same partition can create a hotspot that cannot be resolved by moving the partition elsewhere.

### Proposed Solutions

#### Sub-Partitioning

Split a hot partition into sub-partitions, each handling a subset of workflow keys:

```
  Partition 7 (hot)
  ┌─────────────────────────────────────┐
  │  Before:  all keys → single owner   │
  └─────────────────────────────────────┘
                    │
                    ▼
  ┌──────────┐  ┌──────────┐  ┌──────────┐
  │  Sub 7-0 │  │  Sub 7-1 │  │  Sub 7-2 │
  │  keys    │  │  keys    │  │  keys    │
  │  0x00-54 │  │  0x55-AA │  │  0xAB-FF │
  └──────────┘  └──────────┘  └──────────┘
  Owner: O-1     Owner: O-2     Owner: O-3
```

Sub-partitions use secondary hash ranges and can be distributed across different orchestrators.

#### Key-Based Sharding

For workflows that are individually hot (e.g., one workflow_id generating thousands of tasks), shard by `(workflow_id, task_sequence)` so tasks within a single workflow can be parallelized across owners.

#### Rate Limiting

Apply per-partition and per-workflow rate limits to prevent unbounded task submission:

- Per-workflow: cap task creation rate (e.g., 1000 tasks/sec)
- Per-partition: cap aggregate throughput (e.g., 5000 tasks/sec)
- Excess tasks are queued or rejected with backpressure signals

---

## 4. Adaptive Worker Routing

### Problem

Current routing selects workers based on capability match and static locality tiers (same-pod → same-node → same-zone → any). This ignores runtime conditions like worker load and network latency.

### Proposed Enhancements

#### Latency-Aware Routing

Maintain a **rolling latency histogram** per worker connection. Prefer workers with lower observed round-trip times:

```
  Worker Selection Score:
  score(w) = capability_match(w)
           × locality_weight(w)
           × (1 / p50_latency_ms(w))
           × (1 / active_tasks(w))
```

#### Load-Aware Routing

Track active task count and recent completion rate per worker. Avoid workers that are at or near capacity:

```
  ┌─────────────────────────────────────────┐
  │          Worker Routing Table            │
  │                                         │
  │  Worker   │ Cap  │ Active │ P50ms │ Score│
  │  ─────────┼──────┼────────┼───────┼──────│
  │  svc-a-1  │ pay  │  3/10  │  2.1  │ 0.89│
  │  svc-a-2  │ pay  │  8/10  │  4.3  │ 0.31│
  │  svc-b-1  │ pay  │  1/10  │  1.8  │ 0.95│  ← selected
  └─────────────────────────────────────────┘
```

#### Circuit Breaking

If a worker exceeds error rate thresholds, temporarily remove it from the routing pool:

- **Half-open**: Send a probe task after cooldown
- **Closed**: Resume full routing on success
- **Open**: Skip worker entirely during cooldown

---

## 5. Partition Replication (Warm Standby)

### Problem

Current failover requires lease expiry + full WAL replay on the new owner. For large partitions, replay can take seconds — too slow for latency-sensitive workflows.

### Proposed Solution

Maintain a **warm standby** for each partition that continuously receives WAL entries:

```
  ┌─────────────┐     WAL stream     ┌─────────────┐
  │   Primary    │ ──────────────────▶│   Standby    │
  │  (Owner)     │                    │  (Follower)  │
  │              │                    │              │
  │  In-memory   │                    │  In-memory   │
  │  state: live │                    │  state: warm │
  └─────────────┘                    └─────────────┘
          │                                  │
          │  lease expires                   │
          │◀─────────────────────────────────│
          │                           acquires lease
                                     activates immediately
```

#### Replication Modes

| Mode            | Latency Impact | Durability       | Failover Time |
|-----------------|---------------|------------------|---------------|
| Async (default) | None          | Eventual         | ~100ms        |
| Semi-sync       | +1-2ms        | Bounded lag      | ~50ms         |
| Sync            | +5-10ms       | Strong           | ~10ms         |

#### Tradeoffs

- **Resource cost**: Each standby consumes memory and network bandwidth
- **Consistency**: Async replication may lose the last few WAL entries on failover
- **Complexity**: Standby promotion requires fencing to prevent split-brain

---

## 6. Smart Initial Placement

### Problem

New partitions (or newly registered workflows) are assigned to orchestrators using round-robin or least-loaded heuristics. This ignores domain-level affinity that could improve performance.

### Proposed Solution

Accept **placement hints** at workflow registration time:

```go
client.RegisterWorkflow(ctx, &RegisterRequest{
    WorkflowType: "payment-processing",
    PlacementHints: &PlacementHints{
        PreferredRegion: "us-east-1",
        TenantID:        "tenant-42",
        AffinityGroup:   "payment-service",
    },
})
```

#### Hint Types

| Hint              | Effect                                              |
|-------------------|-----------------------------------------------------|
| `preferred_region`| Place partition in the specified region              |
| `tenant_id`       | Co-locate with other partitions of the same tenant  |
| `affinity_group`  | Co-locate with partitions using the same workers    |
| `anti_affinity`   | Spread across distinct failure domains              |

#### Placement Algorithm

```
  1. Filter: eligible orchestrators (capacity, health)
  2. Score:  region match (+10), tenant co-location (+5),
            affinity group match (+3), load headroom (+2)
  3. Select: highest scored orchestrator
  4. Fallback: least-loaded if no hints match
```

---

## 7. Multi-Tenant Isolation

### Problem

In shared deployments, one tenant's workload can affect another's latency and throughput. There is no mechanism to enforce resource boundaries between tenants.

### Proposed Solution

#### Tenant-Level Partitioning

Dedicate partition ranges to tenants:

```
  ┌──────────────────────────────────────────────┐
  │            Partition Allocation               │
  │                                               │
  │  Tenant A:  Partitions 0-15   (dedicated)     │
  │  Tenant B:  Partitions 16-31  (dedicated)     │
  │  Shared:    Partitions 32-63  (best-effort)   │
  └──────────────────────────────────────────────┘
```

#### Quotas and Rate Limits

| Quota Type           | Scope     | Example                   |
|---------------------|-----------|---------------------------|
| Max active workflows | Tenant    | 10,000                    |
| Max tasks/sec        | Tenant    | 5,000                     |
| Max partition count  | Tenant    | 16                        |
| WAL storage          | Tenant    | 10 GB                     |

#### Isolation Levels

| Level      | Mechanism                | Overhead | Isolation      |
|-----------|--------------------------|----------|----------------|
| Soft       | Priority + rate limiting | Low      | Best-effort    |
| Fair       | Weighted fair queuing    | Medium   | Proportional   |
| Hard       | Dedicated partitions     | High     | Full           |

---

## 8. Advanced Scheduling

### Problem

Current scheduling uses static priority levels (HIGH / MEDIUM / LOW) with fair queuing. This is insufficient for use cases with SLAs, deadlines, or preemption requirements.

### Proposed Enhancements

#### SLA-Based Execution

Attach deadline metadata to tasks:

```
  Task {
    workflow_id: "order-123"
    deadline:    "2025-01-15T10:00:05Z"   // must complete by
    sla_class:   "gold"                    // determines priority boost
  }
```

The scheduler boosts priority as the deadline approaches:

```
  effective_priority = base_priority + deadline_urgency_boost(remaining_time)
```

#### Priority Preemption

For critical tasks, support preempting lower-priority work:

```
  1. CRITICAL task arrives, all workers busy with LOW tasks
  2. Orchestrator sends PREEMPT signal to worker with oldest LOW task
  3. Worker pauses LOW task, checkpoints state
  4. Worker executes CRITICAL task
  5. Worker resumes LOW task from checkpoint
```

#### Deadline-Aware Scheduling

```
  ┌────────────────────────────────────────────────────┐
  │              Scheduler Queue (per partition)        │
  │                                                     │
  │  Priority │ Task        │ Deadline    │ Urgency     │
  │  ─────────┼─────────────┼─────────────┼───────────  │
  │  CRITICAL │ task-091    │ +2s         │ 0.95        │ ← next
  │  HIGH     │ task-088    │ +30s        │ 0.42        │
  │  HIGH     │ task-089    │ +120s       │ 0.15        │
  │  MEDIUM   │ task-090    │ none        │ 0.00        │
  └────────────────────────────────────────────────────┘
```

---

## 9. Observability & Debugging

### Problem

Distributed workflows are inherently difficult to debug. Operators need end-to-end visibility into workflow execution, task timing, and failure attribution.

### Proposed Enhancements

#### Workflow Timeline Visualization

Provide a timeline view of workflow execution:

```
  Workflow: order-123
  ┌──────────────────────────────────────────────────────┐
  │ validate_order  ████░░░░░░░░░░░░░░░░░░░░░░░░  2ms   │
  │ charge_payment  ░░░░████████░░░░░░░░░░░░░░░░  15ms  │
  │ reserve_stock   ░░░░████████░░░░░░░░░░░░░░░░  12ms  │
  │ ship_order      ░░░░░░░░░░░░████████████░░░░  25ms  │
  │ send_receipt    ░░░░░░░░░░░░░░░░░░░░░░░░████  3ms   │
  └──────────────────────────────────────────────────────┘
  Total: 57ms  │  Tasks: 5  │  Retries: 0  │  Status: COMPLETED
```

#### Distributed Tracing (OpenTelemetry)

Integrate OpenTelemetry for end-to-end trace propagation:

- **Trace context**: Propagate `trace_id` and `span_id` through gRPC metadata
- **Spans**: Emit spans for task dispatch, worker execution, WAL writes, and state transitions
- **Exporters**: Support OTLP, Jaeger, and Zipkin

```
  Trace: abc-123
  ├── Gateway.SubmitTask          [1.2ms]
  │   └── Orchestrator.Enqueue    [0.3ms]
  │       ├── WAL.Append          [0.1ms]
  │       ├── Scheduler.Enqueue   [0.05ms]
  │       └── Worker.Execute      [14.8ms]
  │           └── handler()       [14.2ms]
  └── Orchestrator.Complete       [0.2ms]
      └── WAL.Append              [0.1ms]
```

#### Debugging Tools

- **Task Inspector**: Query task state, history, and retry count via API
- **Partition Inspector**: View partition ownership, WAL size, queue depth
- **Replay Mode**: Re-execute a workflow from WAL for debugging without side effects
- **Dead Letter Queue**: Surface tasks that exhausted retries for manual inspection

---

## 10. Backpressure & Flow Control

### Problem

When workers are overwhelmed or downstream services slow down, the orchestrator continues dispatching tasks. Without backpressure, this leads to unbounded queue growth, memory pressure, and cascading failures.

### Proposed Solution

#### Layered Backpressure

```
  ┌──────────┐    ┌──────────────┐    ┌──────────┐
  │  Client   │◀───│ Orchestrator │◀───│  Worker   │
  │           │ BP │              │ BP │           │
  └──────────┘    └──────────────┘    └──────────┘

  BP = backpressure signal (propagated upstream)
```

**Worker → Orchestrator**:
- Workers report queue depth and active task count in heartbeats
- When a worker's active tasks exceed capacity, orchestrator stops dispatching to it
- gRPC flow control (HTTP/2 window) limits in-flight bytes

**Orchestrator → Client**:
- When partition queue depth exceeds threshold, return `RESOURCE_EXHAUSTED` to the gateway
- Gateway propagates rejection to clients with retry-after header

#### Adaptive Dispatch Rate

```
  dispatch_rate = min(
    worker_capacity - worker_active_tasks,
    partition_queue_limit - partition_queue_depth,
    global_rate_limit
  )
```

#### Circuit Breaker at Orchestrator

If a partition's error rate exceeds threshold, temporarily stop dispatching:

- Prevents sending tasks to a broken downstream
- Gives workers time to recover
- Auto-resumes after cooldown with probe tasks

---

## 11. State Compaction & Archival

### Problem

The WAL grows unbounded as workflows execute. Completed workflows remain in the WAL indefinitely, consuming storage and increasing replay time on recovery.

### Proposed Solution

#### WAL Compaction

Periodically compact the WAL by removing events for completed workflows:

```
  Before compaction:
  ┌──────────────────────────────────────────────┐
  │  WAL Segment 0-99                            │
  │  [TaskCreated][TaskDispatched][TaskCompleted] │  ← workflow done
  │  [TaskCreated][TaskDispatched]                │  ← in-flight
  │  [TaskCreated][TaskCompleted]                 │  ← workflow done
  └──────────────────────────────────────────────┘

  After compaction:
  ┌──────────────────────────────────────────────┐
  │  WAL Segment 0-99 (compacted)                │
  │  [TaskCreated][TaskDispatched]                │  ← in-flight only
  └──────────────────────────────────────────────┘
  
  Archived:
  ┌──────────────────────────────────────────────┐
  │  Cold Storage (S3 / GCS)                     │
  │  workflow-abc.wal.gz                          │
  │  workflow-def.wal.gz                          │
  └──────────────────────────────────────────────┘
```

#### Compaction Strategy

| Strategy         | Trigger                     | Impact                        |
|-----------------|-----------------------------|------------------------------ |
| Time-based       | Every N hours               | Predictable, simple           |
| Size-based       | WAL exceeds N MB            | Prevents unbounded growth     |
| Completion-based | After workflow completes    | Immediate cleanup             |

#### Cold Storage Archival

- Completed workflow WAL segments are compressed and uploaded to object storage (S3, GCS, Azure Blob)
- Archived workflows can be queried for audit or replayed for debugging
- Retention policies: configurable per tenant or globally (e.g., 30 days, 1 year)

---

## 12. Multi-Cluster / Multi-Region

### Problem

A single Kubernetes cluster is a single failure domain. For disaster recovery and global latency optimization, Ortrix must support geo-distributed operation.

### Proposed Solution

#### Federation Model

```
  ┌─────────────────────┐         ┌─────────────────────┐
  │    Region: US-East   │         │   Region: EU-West    │
  │                      │         │                      │
  │  ┌──────────────┐   │  async  │  ┌──────────────┐    │
  │  │ Orchestrator  │───┼────────┼──│ Orchestrator  │    │
  │  │ Cluster A     │   │  WAL   │  │ Cluster B     │    │
  │  └──────────────┘   │  repl   │  └──────────────┘    │
  │  ┌──────────────┐   │         │  ┌──────────────┐    │
  │  │   Workers     │   │         │  │   Workers     │    │
  │  └──────────────┘   │         │  └──────────────┘    │
  └─────────────────────┘         └─────────────────────┘
              │                              │
              └──────────┬───────────────────┘
                         │
                  ┌──────▼──────┐
                  │   Global    │
                  │  Metadata   │
                  │  (etcd/CRD) │
                  └─────────────┘
```

#### Failover Strategies

| Strategy              | RPO        | RTO        | Complexity |
|----------------------|------------|------------|------------|
| Active-passive        | Minutes    | Minutes    | Low        |
| Active-active (async) | Seconds    | Seconds    | Medium     |
| Active-active (sync)  | Zero       | Seconds    | High       |

#### Design Considerations

- **Partition affinity**: Prefer executing partitions in their home region
- **Cross-region dispatch**: Route tasks to remote region only when local workers lack capability
- **Conflict resolution**: Last-writer-wins for async replication; deterministic merge for active-active
- **Global routing table**: A lightweight global metadata store (etcd, Kubernetes CRDs) maps partitions to regions
- **Data sovereignty**: Respect per-tenant region constraints for regulatory compliance

---

## Prioritization

The following is the suggested implementation order based on impact and dependencies:

| Priority | Feature                            | Depends On          |
|----------|------------------------------------|---------------------|
| P0       | Load-based rebalancing             | Partition migration  |
| P0       | Partition migration (locality)     | —                   |
| P1       | Hot partition mitigation           | Sub-partitioning    |
| P1       | Partition replication              | WAL streaming       |
| P1       | Backpressure & flow control        | —                   |
| P2       | Adaptive worker routing            | Latency tracking    |
| P2       | Observability (OpenTelemetry)      | —                   |
| P2       | State compaction & archival        | —                   |
| P3       | Smart initial placement            | Placement hints API |
| P3       | Multi-tenant isolation             | Partition allocation|
| P3       | Advanced scheduling                | Deadline metadata   |
| P3       | Multi-cluster / multi-region       | Replication         |
