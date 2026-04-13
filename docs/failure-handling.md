# Failure Handling

Flowd is designed to tolerate failures at every layer. This document covers failure modes, detection mechanisms, and recovery strategies.

## Partition Crash (Orchestrator Failure)

When an orchestrator instance crashes, its partitions become orphaned until another instance takes over.

### Detection

```
  orchestrator-1 crashes
       │
       ▼ (lease not renewed)
       │
  T+15s: Lease expires in coordination store
       │
       ▼
  orchestrator-2 detects free partitions via watch
       │
       ▼
  orchestrator-2 acquires leases (epoch incremented)
```

### Recovery Steps

```
1. Acquire partition lease with new epoch (fencing token)
2. Load latest snapshot for the partition
3. Replay WAL entries after the snapshot sequence number
4. Reconstruct in-memory state:
   - Active tasks and their states
   - In-flight task assignments
   - Pending queue contents
5. Re-evaluate in-flight tasks:
   - Tasks marked DISPATCHED → check if worker is still alive
   - If worker alive → wait for result (task may complete normally)
   - If worker dead → re-enqueue task for retry
6. Resume accepting new tasks
```

### Guarantees During Partition Crash

| Guarantee                | Mechanism                                |
|--------------------------|------------------------------------------|
| No data loss             | WAL is durable (local + async replicated)|
| No duplicate execution   | Idempotency keys on tasks                |
| Bounded unavailability   | Lease expiry + recovery time (~16s)      |
| Correct state recovery   | Event-sourced replay is deterministic    |

## Worker Crash

When a worker (service with embedded SDK) crashes, the orchestrator detects it through heartbeat failure and stream disconnection.

### Detection

```
  Worker heartbeat interval: 5s
  Heartbeat timeout: 15s (3 missed heartbeats)

  T+0s  : Worker sends heartbeat
  T+5s  : Expected heartbeat — not received
  T+10s : Expected heartbeat — not received
  T+15s : Worker marked DEAD
```

The gRPC stream also provides immediate detection — stream closure triggers an event on the orchestrator side.

### Recovery Steps

```
1. Stream closes → orchestrator detects disconnection
2. Mark worker as UNAVAILABLE in capability index
3. For each in-flight task assigned to the dead worker:
   a. Check if task result was already received → skip
   b. Increment retry counter
   c. If retries < max_retries:
      - WAL.Append(TaskRetrying)
      - Re-enqueue task in scheduler
   d. If retries >= max_retries:
      - WAL.Append(TaskFailed)
      - Trigger compensation if saga
4. Remove worker from capability index
5. If worker reconnects later:
   - Re-register capabilities
   - Resume receiving new tasks
```

### Worker Crash vs Worker Slowness

| Signal               | Interpretation                             | Action                    |
|----------------------|--------------------------------------------|---------------------------|
| Stream closed        | Worker crashed or network partition         | Immediate re-dispatch     |
| Heartbeat timeout    | Worker unresponsive (may be alive)          | Mark dead, re-dispatch    |
| Task timeout         | Worker alive but task stuck                 | Cancel task, re-dispatch  |
| Slow heartbeat       | Worker under load but functional            | Reduce task allocation    |

## In-Flight Task Handling

Tasks that are dispatched but not yet completed require special handling during failures.

### Task States During Failure

```
  DISPATCHED ──worker crash──▶ needs re-dispatch
  RUNNING    ──worker crash──▶ needs re-dispatch (may have side effects)
  DISPATCHED ──orch crash───▶ recovered from WAL, re-evaluated
  RUNNING    ──orch crash───▶ recovered from WAL, worker may complete
```

### Re-Dispatch Logic

```
  On orchestrator recovery:
    for each task in DISPATCHED or RUNNING state:
      if assigned_worker is connected:
        // Worker may still complete it — wait
        set task_timeout = now + task_timeout_duration
      else:
        // Worker is gone — safe to re-dispatch
        if task.retries < max_retries:
          WAL.Append(TaskRetrying)
          scheduler.Enqueue(task)
        else:
          WAL.Append(TaskFailed{reason: "max retries exceeded"})
```

### Timeout-Based Recovery

Even if a worker is alive, tasks can get stuck. Each task has a configurable timeout:

```
  Task dispatched at T+0
  Timeout: 30s

  T+30s: No result received
  → WAL.Append(TaskFailed{reason: "timeout"})
  → Re-enqueue if retries available
```

## Idempotency Strategy

Since tasks may be dispatched more than once (at-least-once delivery), handlers must be **idempotent**.

### Idempotency Key

Every task carries a unique **idempotency key** in its metadata:

```
Task{
  id: "task-abc-123",
  metadata: {
    "idempotency_key": "payment-order-456-attempt-1",
  }
}
```

### Worker-Side Idempotency

Workers are responsible for checking idempotency:

```go
func handlePayment(ctx context.Context, taskID string, payload []byte) ([]byte, error) {
    key := extractIdempotencyKey(ctx)

    // Check if already processed
    if result, ok := idempotencyStore.Get(key); ok {
        return result, nil  // Return cached result
    }

    // Process payment
    result, err := processPayment(payload)
    if err != nil {
        return nil, err
    }

    // Store result for idempotency
    idempotencyStore.Set(key, result, 24*time.Hour)
    return result, nil
}
```

### Orchestrator-Side Deduplication

The orchestrator also tracks completed task IDs to avoid re-dispatching tasks that already have results:

```
  Task "abc-123" dispatched to worker-1
  Worker-1 crashes after completing but before sending result
  Task "abc-123" re-dispatched to worker-2
  Worker-2 checks idempotency key → already processed → returns cached result
```

### Idempotency Guarantees

| Layer         | Mechanism                                  |
|---------------|-------------------------------------------|
| Orchestrator  | Dedup by task ID in WAL                   |
| Worker        | Idempotency key check before execution    |
| External      | Idempotency key passed to external systems|

## Reconciliation for External Systems

For tasks that interact with external systems (payment processors, APIs), failure handling requires special care because **you cannot roll back an external side effect**.

### The Problem

```
  Task: charge customer $100
  1. Worker sends charge to Stripe  ✓ (money charged)
  2. Worker crashes before reporting result
  3. Task re-dispatched to new worker
  4. New worker charges customer $100 again  ✗ (double charge!)
```

### Solution: Idempotency + Reconciliation

**Prevention (Idempotency)**:

```
  // Pass idempotency key to external system
  stripe.Charge(amount=100, idempotency_key="order-456")
  // Stripe deduplicates: second call returns same result
```

**Detection (Reconciliation)**:

For systems that don't support idempotency natively, run periodic reconciliation:

```
  ┌─────────────┐     ┌──────────────┐     ┌──────────────┐
  │ Flowd State  │────▶│ Reconciler   │◀────│ External     │
  │ (expected)   │     │              │     │ System State │
  └─────────────┘     └──────┬───────┘     └──────────────┘
                              │
                         diff detected
                              │
                         ┌────▼────┐
                         │ Action  │
                         │ - alert │
                         │ - fix   │
                         │ - log   │
                         └─────────┘
```

### Saga Pattern for Compensation

For multi-step workflows, Flowd supports the **saga pattern**. Each step has a corresponding **compensation action** that undoes its effect:

```
  Workflow: Book Travel
    Step 1: Reserve flight      ← compensate: Cancel flight
    Step 2: Reserve hotel       ← compensate: Cancel hotel
    Step 3: Charge payment      ← compensate: Refund payment

  If Step 3 fails:
    Compensate Step 2: Cancel hotel
    Compensate Step 1: Cancel flight
```

### Compensation Flow

```
  Step 1: OK
  Step 2: OK
  Step 3: FAILED
       │
       ▼
  WAL.Append(TaskCompensating{step=2})
  Execute: Cancel hotel
  WAL.Append(TaskCompensated{step=2})
       │
       ▼
  WAL.Append(TaskCompensating{step=1})
  Execute: Cancel flight
  WAL.Append(TaskCompensated{step=1})
       │
       ▼
  Workflow state: COMPENSATED
```

Compensation actions are themselves idempotent and retriable. If compensation fails, it is retried with the same idempotency guarantees.

## Failure Summary Matrix

| Failure Mode         | Detection           | Recovery                    | Data Loss | Downtime      |
|---------------------|---------------------|-----------------------------|-----------|---------------|
| Orchestrator crash  | Lease expiry (15s)  | Snapshot + WAL replay       | None      | ~16s          |
| Worker crash        | Stream close / HB   | Re-dispatch to other worker | None      | ~0s (instant) |
| Network partition   | Heartbeat timeout   | Re-dispatch + idempotency   | None      | ~15s          |
| Task timeout        | Timer expiry        | Re-dispatch or fail         | None      | Task timeout  |
| WAL corruption      | Checksum validation | Replay from last snapshot   | Minimal   | Recovery time |
| External system     | Task failure        | Retry + reconciliation      | None      | Retry delay   |
