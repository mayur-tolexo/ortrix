# Partitioning and Scaling

## Partition Ownership

Every workflow in Flowd is assigned to a **partition**, and every partition has exactly **one owner** (an orchestrator instance). This single-owner model eliminates distributed coordination on the hot path.

```
  workflow_id ──hash──▶ partition_id ──lease──▶ orchestrator instance
```

### Partition Assignment

```
  partition_id = hash(workflow_id) % total_partitions
```

- Uses consistent hashing (FNV or xxHash) for uniform distribution
- `total_partitions` is fixed at cluster creation (e.g., 256)
- Partitions far exceed orchestrator count to allow fine-grained rebalancing

### Ownership Table

The ownership table is stored in a coordination service (etcd / Kubernetes lease objects):

```
┌───────────┬──────────────────┬──────────────────────┐
│ Partition │ Owner            │ Lease Expiry         │
├───────────┼──────────────────┼──────────────────────┤
│ 0         │ orchestrator-0   │ 2025-01-15T10:30:00Z │
│ 1         │ orchestrator-0   │ 2025-01-15T10:30:00Z │
│ 2         │ orchestrator-1   │ 2025-01-15T10:30:02Z │
│ 3         │ orchestrator-1   │ 2025-01-15T10:30:02Z │
│ 4         │ orchestrator-2   │ 2025-01-15T10:30:01Z │
│ ...       │ ...              │ ...                  │
└───────────┴──────────────────┴──────────────────────┘
```

## Lease Mechanism

Partition ownership uses **time-bounded leases** to prevent split-brain and enable automatic failover.

### Lease Lifecycle

```
  ┌──────────┐    acquire    ┌──────────┐    renew     ┌──────────┐
  │  FREE    │──────────────▶│  OWNED   │─────────────▶│  OWNED   │
  └──────────┘               └──────────┘              └──────────┘
                                  │                         │
                            expire/release            expire/release
                                  │                         │
                                  ▼                         ▼
                             ┌──────────┐              ┌──────────┐
                             │  FREE    │              │  FREE    │
                             └──────────┘              └──────────┘
```

### Lease Parameters

| Parameter        | Default | Description                              |
|------------------|---------|------------------------------------------|
| Lease Duration   | 15s     | Time before lease expires if not renewed |
| Renewal Interval | 5s      | How often the owner renews the lease     |
| Grace Period     | 3s      | Buffer before another node can acquire   |

### Lease Protocol

1. **Acquire**: Orchestrator writes `{owner, expiry}` to coordination store (atomic CAS)
2. **Renew**: Owner periodically extends expiry (must succeed before current expiry)
3. **Release**: Owner explicitly releases on graceful shutdown
4. **Expire**: If renewal fails (crash, network partition), lease expires naturally

### Fencing

To prevent stale owners from writing after lease expiry:

- Each lease has a **monotonic epoch** (fencing token)
- All WAL writes include the epoch
- WAL rejects writes with stale epochs
- New owner increments epoch on acquisition

```
  orchestrator-1 (epoch=5): WAL.Append(..., epoch=5)  ✓
  orchestrator-1 crashes, lease expires
  orchestrator-2 acquires (epoch=6)
  orchestrator-1 recovers: WAL.Append(..., epoch=5)   ✗ rejected
```

## Rebalancing

When orchestrator instances are added or removed, partitions must be redistributed.

### Triggers

- New orchestrator instance joins the cluster
- Existing orchestrator instance leaves (graceful or crash)
- Load imbalance detected (one orchestrator has disproportionate load)
- Operator-initiated rebalance

### Rebalancing Process

```
  Phase 1: Compute new assignment
  ┌─────────────────────────────────────────────┐
  │  Current:  O1=[P0,P1,P2,P3]  O2=[P4,P5,P6,P7]  │
  │  Target:   O1=[P0,P1,P2]  O2=[P4,P5,P6]  O3=[P3,P7]  │
  └─────────────────────────────────────────────┘

  Phase 2: Drain partitions being moved
  ┌─────────────────────────────────────────────┐
  │  O1: Stop accepting new tasks for P3         │
  │  O1: Wait for in-flight tasks on P3          │
  │  O1: Flush WAL for P3                        │
  │  O1: Release lease for P3                    │
  └─────────────────────────────────────────────┘

  Phase 3: New owner takes over
  ┌─────────────────────────────────────────────┐
  │  O3: Acquire lease for P3                    │
  │  O3: Load snapshot + replay WAL for P3       │
  │  O3: Begin accepting tasks for P3            │
  └─────────────────────────────────────────────┘
```

### Rebalancing Guarantees

- **No task loss**: WAL ensures all events are durable before handoff
- **Minimal disruption**: Only moved partitions experience brief unavailability
- **At-most-one-owner**: Lease fencing ensures no two owners write simultaneously
- **Gradual**: Partitions move one at a time to limit blast radius

## Failover Handling

When an orchestrator crashes, its partitions become available for takeover.

### Failure Detection

```
  orchestrator-1 crashes
       │
       ▼
  Lease for P0 expires (15s TTL)
       │
       ▼
  orchestrator-2 detects free partition
       │
       ▼
  orchestrator-2 acquires lease for P0 (epoch+1)
       │
       ▼
  orchestrator-2 recovers P0:
    1. Load latest snapshot
    2. Replay WAL from snapshot sequence
    3. Re-dispatch in-flight tasks
    4. Resume normal operation
```

### Failover Timeline

```
T+0s   : orchestrator-1 crashes
T+15s  : Lease for partitions expires
T+15.5s: orchestrator-2 acquires leases
T+16s  : Snapshot loaded, WAL replay begins
T+16.2s: Partition ready (assuming 10K events to replay)
```

Total failover time: **~16 seconds** (dominated by lease expiry)

### Reducing Failover Time

| Strategy                | Effect                           |
|------------------------|----------------------------------|
| Shorter lease duration | Faster detection (risk: false positives) |
| Health-check-based     | Proactive detection before lease expires |
| Warm standby           | Pre-load snapshots on standby nodes      |
| Smaller partitions     | Less state to recover per partition       |

## Horizontal Scaling

### Scaling Orchestrators

Adding orchestrator instances is straightforward:

```
  Before: 2 orchestrators, 256 partitions (128 each)
  After:  4 orchestrators, 256 partitions (64 each)
```

1. New orchestrator joins the cluster
2. Rebalancing assigns a subset of partitions to it
3. It loads snapshots and replays WAL for acquired partitions
4. Existing orchestrators release their excess partitions

### Scaling Workers

Workers scale independently since they connect to orchestrators via gRPC streams:

1. New service instance starts with embedded Worker SDK
2. SDK connects to orchestrator, registers capabilities
3. Orchestrator immediately considers it for task dispatch
4. No rebalancing required — new worker is additive capacity

### Scaling Limits

| Component     | Scaling Factor                              | Limit                           |
|--------------|---------------------------------------------|---------------------------------|
| Partitions   | Fixed at creation                           | Choose wisely (256–4096)        |
| Orchestrators| Up to partition count                       | Cannot exceed partition count   |
| Workers      | Unlimited (per orchestrator stream limit)   | gRPC stream capacity            |
| Throughput   | Linear with orchestrators (partitions independent) | WAL write throughput       |

### Auto-Scaling

Flowd integrates with Kubernetes HPA for automatic scaling:

- **Orchestrators**: Scale based on partition load (tasks/second per partition)
- **Workers**: Scale based on task queue depth or processing latency
- Rebalancing is triggered automatically when orchestrator count changes
