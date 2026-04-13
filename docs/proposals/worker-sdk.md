# Proposal: Worker SDK

## Table of Contents

- [Status](#status)
- [Problem Statement](#problem-statement)
- [Motivation](#motivation)
- [Design Details](#design-details)
  - [SDK Design](#sdk-design)
  - [Handler Registration](#handler-registration)
  - [Retry and Rate Limiting](#retry-and-rate-limiting)
- [Alternatives Considered](#alternatives-considered)
- [Tradeoffs](#tradeoffs)
- [Testing Strategy](#testing-strategy)
- [Rollout Plan](#rollout-plan)

---

## Status

**Proposed**

---

## Problem Statement

Ortrix workers are not standalone services — the Worker SDK is embedded directly into existing Go services. The SDK must provide a clean, minimal API for registering task handlers, managing connections to the orchestrator, handling retries, and enforcing rate limits. A well-designed SDK is critical for developer adoption and operational reliability.

---

## Motivation

- **Developer experience**: The SDK is the primary interface for Ortrix users. A clean, ergonomic API lowers the barrier to adoption.
- **Reliability**: Built-in retry logic and rate limiting prevent individual handlers from destabilizing the worker or orchestrator.
- **Consistency**: A standardized SDK ensures all workers behave uniformly in terms of connection management, heartbeats, and error reporting.
- **Extensibility**: The SDK must support future features (metrics, tracing, middleware) without breaking existing users.

---

## Design Details

### SDK Design

The SDK follows a builder-pattern configuration model with sensible defaults:

```go
package main

import (
    "context"
    "github.com/mayur-tolexo/ortrix/pkg/sdk"
)

func main() {
    w := sdk.NewWorker("payment-service",
        sdk.WithMaxConcurrency(50),
        sdk.WithHeartbeatInterval(5 * time.Second),
        sdk.WithReconnectBackoff(100*time.Millisecond, 30*time.Second),
    )

    w.RegisterHandler("process_payment", handlePayment)
    w.RegisterHandler("refund_payment", handleRefund)

    // Blocks until context is cancelled or fatal error
    w.Start(ctx, "orchestrator:9090")
}
```

**Core components:**

| Component          | Responsibility                                      |
|-------------------|-----------------------------------------------------|
| Worker             | Top-level struct, manages lifecycle                  |
| StreamManager      | gRPC stream connection, reconnection, keepalive      |
| HandlerRegistry    | Maps task types to handler functions                 |
| Dispatcher         | Receives tasks from stream, routes to handlers       |
| HeartbeatLoop      | Sends periodic heartbeats with load metrics          |
| RetryManager       | Manages per-handler retry policies                   |
| RateLimiter        | Enforces per-handler and global rate limits           |

**Configuration options:**

| Option                  | Default   | Description                              |
|------------------------|-----------|------------------------------------------|
| `MaxConcurrency`        | 100       | Max concurrent tasks across all handlers |
| `HeartbeatInterval`     | 5s        | Heartbeat send frequency                 |
| `ReconnectBackoffMin`   | 100ms     | Initial reconnection backoff             |
| `ReconnectBackoffMax`   | 30s       | Maximum reconnection backoff             |
| `TaskTimeout`           | 30s       | Default per-task execution timeout       |
| `GracefulShutdownTimeout` | 15s     | Time to finish tasks on shutdown         |

### Handler Registration

Handlers are registered by task type with optional per-handler configuration:

```go
// Simple registration
w.RegisterHandler("send_email", handleSendEmail)

// Registration with options
w.RegisterHandler("process_payment", handlePayment,
    sdk.WithHandlerTimeout(60 * time.Second),
    sdk.WithHandlerRetries(3),
    sdk.WithHandlerRateLimit(100),  // max 100 tasks/sec
)
```

**Handler signature:**

```go
type TaskHandler func(ctx context.Context, taskID string, payload []byte) ([]byte, error)
```

- `ctx` carries deadline, cancellation, trace context, and idempotency key
- `taskID` is the unique task identifier for logging and correlation
- `payload` is the raw task input (JSON, Protobuf, or any serialization)
- Return `(result, nil)` on success, `(nil, error)` on failure
- The SDK wraps the result in a `TaskResult` message and sends it on the stream

**Handler lifecycle:**

```
Task received from stream
  │
  ├─ Check concurrency limit → reject if at capacity
  │
  ├─ Check rate limit → queue or reject if exceeded
  │
  ├─ Create context with timeout and trace propagation
  │
  ├─ Execute handler function
  │
  ├─ On success → send TaskResult(success=true) on stream
  │
  └─ On failure → check retry policy
       ├─ Retries remaining → return error (orchestrator re-dispatches)
       └─ No retries → send TaskResult(success=false, error=...)
```

### Retry and Rate Limiting

**Retry policy:**

Retries are primarily orchestrator-driven (re-dispatch to potentially different workers), but the SDK supports local retries for transient errors:

| Retry Type       | Owner          | Use Case                              |
|-----------------|---------------|---------------------------------------|
| Re-dispatch      | Orchestrator   | Worker crash, timeout, task failure   |
| Local retry      | SDK            | Transient errors (network blip)       |

Local retry configuration:

```go
sdk.WithHandlerRetries(3)                    // max 3 local retries
sdk.WithHandlerRetryBackoff(100*time.Millisecond)  // initial backoff
```

Local retries use exponential backoff with jitter. Only errors wrapped with `sdk.Retryable(err)` trigger local retries — all other errors are returned to the orchestrator immediately.

**Rate limiting:**

Rate limiting prevents a single handler from consuming all worker capacity:

```go
sdk.WithHandlerRateLimit(100)   // max 100 task starts/sec for this handler
sdk.WithGlobalRateLimit(500)    // max 500 task starts/sec across all handlers
```

Implementation uses a token bucket algorithm:
- Per-handler token bucket refills at the configured rate
- Global token bucket shared across all handlers
- Excess tasks are queued (bounded) or rejected with backpressure signal
- Rate limits are reported in heartbeat messages for orchestrator awareness

---

## Alternatives Considered

| Alternative                  | Pros                           | Cons                                     | Why Not                                    |
|-----------------------------|-------------------------------|------------------------------------------|-------------------------------------------|
| Standalone worker process    | Isolated failures             | Extra infrastructure, deployment overhead| Violates embedded model philosophy         |
| Code generation from proto   | Type-safe, auto-generated     | Complex toolchain, less flexibility      | Go reflection + generics are sufficient    |
| Annotation-based registration| Declarative, clean            | Requires code scanning, magic            | Explicit registration is clearer           |

---

## Tradeoffs

- **Simplicity vs flexibility**: Sensible defaults make the common case easy, but advanced users need escape hatches (custom backoff, middleware, interceptors).
- **Local retries vs orchestrator retries**: Local retries are faster but can mask persistent failures. Orchestrator retries enable cross-worker failover.
- **Rate limiting granularity**: Per-handler limits are precise but add configuration burden. Global limits are simpler but less fair.
- **Embedded vs separate**: The embedded model reduces infrastructure but couples worker lifecycle to the host service.

---

## Testing Strategy

- **Unit tests**: Test handler registration, dispatch routing, retry logic, rate limiter, configuration validation
- **Integration tests**: Full SDK-to-orchestrator task lifecycle over gRPC streams
- **Failure tests**: Handler panics, handler timeouts, orchestrator disconnection during task execution
- **Rate limiting tests**: Verify tasks are throttled at configured rate, verify backpressure propagation
- **Concurrency tests**: Run at max concurrency, verify no race conditions (use `-race` flag)

---

## Rollout Plan

1. **Phase 1**: Core SDK with handler registration, stream management, and heartbeats. Minimal configuration with sensible defaults.
2. **Phase 2**: Add per-handler retry and rate limiting. Include local retry with backoff and token bucket rate limiter.
3. **Phase 3**: Add middleware/interceptor support for tracing, metrics, and custom pre/post-processing. Publish SDK documentation and examples.
