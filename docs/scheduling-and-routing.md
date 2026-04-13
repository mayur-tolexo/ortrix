# Scheduling and Routing

## Capability-Based Routing

Flowd routes tasks to workers based on **capabilities**, not static service names. Each worker declares what task types it can handle, and the orchestrator matches tasks to capable workers dynamically.

### How It Works

```
  Worker Registration:
    Service A → capabilities: ["process_payment", "refund_payment"]
    Service B → capabilities: ["send_email", "send_sms"]
    Service C → capabilities: ["process_payment"]  (duplicate capability)

  Task Routing:
    Task{type="process_payment"} → routed to Service A or Service C
    Task{type="send_email"}      → routed to Service B
```

### Why Not Static Binding

| Approach         | Problem                                              |
|------------------|------------------------------------------------------|
| Static binding   | Hardcoded service → task mapping, brittle to change  |
| Service discovery| Knows which services exist, not what they can do     |
| Capability-based | Workers self-declare, orchestrator routes dynamically|

### Capability Registration

When a worker connects via gRPC stream, it sends a `WorkerRegistration` message:

```protobuf
message WorkerRegistration {
  string worker_id = 1;
  repeated string capabilities = 2;  // ["process_payment", "refund_payment"]
}
```

The orchestrator maintains a **capability index**:

```
┌──────────────────┬─────────────────────────────────┐
│ Capability       │ Workers                         │
├──────────────────┼─────────────────────────────────┤
│ process_payment  │ [svc-a-1, svc-a-2, svc-c-1]    │
│ refund_payment   │ [svc-a-1, svc-a-2]              │
│ send_email       │ [svc-b-1, svc-b-2, svc-b-3]    │
│ send_sms         │ [svc-b-1, svc-b-2, svc-b-3]    │
└──────────────────┴─────────────────────────────────┘
```

### Versioning and Rollout

Capabilities support versioned deployments:

**Canary Rollout**:
```
  svc-a-v1 → capabilities: ["process_payment@v1"]
  svc-a-v2 → capabilities: ["process_payment@v2"]

  Routing rule: 95% → v1, 5% → v2
```

**Blue-Green Deployment**:
```
  Blue  (current): svc-a-blue  → capabilities: ["process_payment"]
  Green (new):     svc-a-green → capabilities: ["process_payment"]

  Cutover: drain blue, switch traffic to green
```

The orchestrator supports routing rules that control traffic distribution across capability versions.

## Locality-Aware Scheduling

When multiple workers can handle a task, Flowd prefers workers that are **topologically close** to the orchestrator or to the data the task needs.

### Locality Tiers

```
  Preference Order (highest to lowest):

  1. Same Pod     (co-located sidecar)
  2. Same Node    (same Kubernetes node)
  3. Same Zone    (same availability zone)
  4. Any          (any available worker)
```

### How Locality Is Determined

Workers report their topology during registration (extracted from Kubernetes downward API):

```
Worker Metadata:
  worker_id: "svc-a-pod-xyz"
  node: "node-3"
  zone: "us-east-1a"
  pod: "svc-a-pod-xyz"
```

The orchestrator uses this metadata to compute **locality scores**:

```
┌──────────────────────────────────────────────────┐
│  Task: process_payment                           │
│  Orchestrator: node-3, zone us-east-1a           │
│                                                  │
│  Candidate Workers:                              │
│    svc-a-1: node-3, zone us-east-1a → score: 3  │
│    svc-a-2: node-5, zone us-east-1a → score: 2  │
│    svc-c-1: node-8, zone us-west-2b → score: 1  │
│                                                  │
│  Selected: svc-a-1 (highest locality score)      │
└──────────────────────────────────────────────────┘
```

### Scoring Algorithm

```
score = 0
if worker.pod == task.affinity_pod:  score += 4
if worker.node == orchestrator.node: score += 3
if worker.zone == orchestrator.zone: score += 2
score += 1  // base score (any worker is better than none)
```

Ties are broken by worker load (prefer least-loaded worker).

### When Locality Is Ignored

- No capable workers exist in preferred locality → fall back to next tier
- All local workers are overloaded → dispatch to remote worker
- Task has explicit routing hints (e.g., `metadata["target_zone"] = "us-west-2"`)

## Worker Selection Strategy

Given a task, the orchestrator selects a worker through a multi-step process:

```
  1. Filter by Capability
     All workers that declare the required task type

  2. Filter by Health
     Remove workers with missed heartbeats

  3. Filter by Capacity
     Remove workers at max concurrent task limit

  4. Score by Locality
     Prefer topologically closer workers

  5. Score by Load
     Prefer workers with fewer in-flight tasks

  6. Apply Routing Rules
     Canary/blue-green traffic splitting

  7. Select Best
     Highest combined score wins
```

### Selection Example

```
Task: {type: "process_payment", priority: HIGH}

Step 1 - Capability Filter:
  ✓ svc-a-1 (process_payment)
  ✓ svc-a-2 (process_payment)
  ✓ svc-c-1 (process_payment)
  ✗ svc-b-1 (send_email only)

Step 2 - Health Filter:
  ✓ svc-a-1 (last heartbeat 2s ago)
  ✗ svc-a-2 (last heartbeat 45s ago — stale)
  ✓ svc-c-1 (last heartbeat 1s ago)

Step 3 - Capacity Filter:
  ✓ svc-a-1 (3/10 slots used)
  ✓ svc-c-1 (9/10 slots used)

Step 4 - Locality Score:
  svc-a-1: same zone → 2
  svc-c-1: different zone → 1

Step 5 - Load Score:
  svc-a-1: 70% free → 0.7
  svc-c-1: 10% free → 0.1

Final: svc-a-1 selected (score: 2.7 vs 1.1)
```

## Priority Queues and Fairness

Tasks are enqueued with a priority level. The scheduler ensures high-priority tasks are dispatched first while preventing starvation of lower-priority tasks.

### Priority Levels

| Level   | Value | Use Case                               |
|---------|-------|----------------------------------------|
| HIGH    | 2     | User-facing, latency-sensitive         |
| MEDIUM  | 1     | Standard business logic                |
| LOW     | 0     | Background jobs, batch processing      |

### Queue Structure

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

### Fairness: Weighted Fair Queuing

Pure priority scheduling would starve LOW tasks indefinitely when HIGH tasks keep arriving. Flowd uses **weighted fair queuing**:

```
  Dispatch ratio:  HIGH : MEDIUM : LOW  =  6 : 3 : 1

  In every 10 dispatch cycles:
    6 tasks from HIGH queue
    3 tasks from MEDIUM queue
    1 task from LOW queue
```

If a queue is empty, its allocation is redistributed to non-empty queues.

### Within-Priority Ordering

Within the same priority level, tasks are ordered by:

1. **Arrival time** (FIFO) — default
2. **Deadline** — if the task has a deadline in metadata, earlier deadlines go first

### Backpressure

When all workers for a capability are at capacity:

- Tasks remain in the priority queue
- No new dispatches for that capability
- gRPC flow control prevents the orchestrator from overwhelming workers
- Tasks are not dropped — they wait until capacity is available
- Queue depth metrics are exposed for auto-scaling decisions
