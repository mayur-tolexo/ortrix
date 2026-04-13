# Ortrix Development Roadmap

## Table of Contents

- [Overview](#overview)
- [Phase 1: Core Execution Engine](#phase-1-core-execution-engine)
- [Phase 2: Streaming and Worker SDK](#phase-2-streaming-and-worker-sdk)
- [Phase 3: Reliability](#phase-3-reliability)
- [Phase 4: Scheduling and Routing](#phase-4-scheduling-and-routing)
- [Phase 5: Scalability](#phase-5-scalability)
- [Phase 6: Advanced Features](#phase-6-advanced-features)

---

## Overview

This roadmap defines the phased development plan for Ortrix. Each phase builds on the previous, with clear goals, deliverables, and dependencies. Phases are designed to deliver incremental value — each phase produces a functional, testable system.

---

## Phase 1: Core Execution Engine

**Goal**: Build the foundational execution loop — partition ownership, in-memory task state management, and basic WAL durability.

### Deliverables

| Deliverable                    | Description                                                  |
|-------------------------------|--------------------------------------------------------------|
| Partition Manager              | Lease-based partition ownership with epoch fencing           |
| In-Memory Execution Loop       | Task state machine (PENDING → SCHEDULED → DISPATCHED → COMPLETED) |
| Basic WAL                      | Append-only write-ahead log with segment-based storage       |
| Task State Machine             | State transitions with WAL persistence                       |
| Configuration Framework        | Environment-based configuration with defaults                |
| Structured Logging             | JSON structured logging for all components                   |

### Dependencies

- None (foundational phase)

---

## Phase 2: Streaming and Worker SDK

**Goal**: Enable push-based task dispatch over persistent gRPC streams and provide an embeddable Worker SDK for Go services.

### Deliverables

| Deliverable                    | Description                                                  |
|-------------------------------|--------------------------------------------------------------|
| gRPC Streaming Protocol        | Bidirectional streaming between orchestrator and workers     |
| Worker SDK                     | Embeddable Go SDK with handler registration                  |
| Task Dispatch                  | Push tasks to workers over persistent streams                |
| Result Collection              | Receive task results on the same stream                      |
| Heartbeat and Liveness         | Worker heartbeat protocol with configurable timeout          |
| Connection Management          | Reconnection with exponential backoff and jitter             |

### Dependencies

- Phase 1: Partition Manager (tasks are dispatched per partition)
- Phase 1: Basic WAL (task events are persisted before dispatch)

---

## Phase 3: Reliability

**Goal**: Harden the system for production use with durable WAL, snapshot-based recovery, and comprehensive failure handling.

### Deliverables

| Deliverable                    | Description                                                  |
|-------------------------------|--------------------------------------------------------------|
| WAL Durability Improvements    | Write batching, fsync optimization, LZ4 compression         |
| Snapshot and Replay            | Periodic snapshots with WAL replay for fast recovery         |
| Partition Failover             | Automatic lease expiry detection and partition takeover      |
| Worker Failure Handling        | Re-dispatch on worker crash, heartbeat timeout detection     |
| Idempotency Framework          | Task-level idempotency keys for at-least-once delivery      |
| Saga Compensation              | Multi-step workflow compensation on failure                  |

### Dependencies

- Phase 1: Basic WAL (enhanced in this phase)
- Phase 2: Worker SDK (failure handling requires stream management)
- Phase 2: gRPC Streaming (stream closure triggers re-dispatch)

---

## Phase 4: Scheduling and Routing

**Goal**: Implement intelligent task scheduling with priority queues, fairness guarantees, and locality-aware worker selection.

### Deliverables

| Deliverable                    | Description                                                  |
|-------------------------------|--------------------------------------------------------------|
| Priority Queues                | THREE-level queues (HIGH / MEDIUM / LOW)                     |
| Weighted Fair Queuing          | Configurable dispatch ratios to prevent starvation           |
| Capability-Based Routing       | Dynamic worker selection based on declared capabilities      |
| Locality-Aware Routing         | Prefer same-pod → same-node → same-zone → any               |
| Worker Load Scoring            | Prefer workers with fewer in-flight tasks                    |
| Routing Rules                  | Canary and blue-green traffic splitting                      |

### Dependencies

- Phase 2: Worker SDK (workers report capabilities and topology)
- Phase 2: Heartbeat protocol (provides load metrics for scoring)

---

## Phase 5: Scalability

**Goal**: Enable horizontal scaling through partition rebalancing and load-aware distribution.

### Deliverables

| Deliverable                    | Description                                                  |
|-------------------------------|--------------------------------------------------------------|
| Partition Rebalancing          | Move partitions between orchestrators on scale events        |
| Load-Based Rebalancing         | Detect hot nodes and redistribute partitions automatically   |
| Locality-Based Migration       | Migrate partitions closer to their execution zones           |
| Hot Partition Mitigation       | Sub-partitioning for workflow hotspots                       |
| Auto-Scaling Integration       | Kubernetes HPA integration for orchestrators and workers     |

### Dependencies

- Phase 1: Partition Manager (rebalancing moves partitions)
- Phase 3: Snapshot and Replay (migration requires state transfer)
- Phase 3: WAL Durability (migration requires WAL flush)

---

## Phase 6: Advanced Features

**Goal**: Extend Ortrix with enterprise-grade capabilities for production deployment at scale.

### Deliverables

| Deliverable                    | Description                                                  |
|-------------------------------|--------------------------------------------------------------|
| Partition Replication          | Warm standby with continuous WAL streaming for fast failover |
| Multi-Region Support           | Geo-distributed orchestration with cross-region WAL replication |
| Observability                  | OpenTelemetry integration, distributed tracing, workflow timeline visualization |
| Advanced Scheduling            | SLA-based execution, deadline urgency boost, priority preemption |
| State Compaction and Archival  | WAL compaction for completed workflows, cold storage archival |
| Multi-Tenant Isolation         | Tenant-level partitioning, quotas, and rate limits           |

### Dependencies

- Phase 3: WAL Durability (replication streams WAL entries)
- Phase 5: Partition Rebalancing (multi-region requires partition placement control)
- Phase 4: Scheduling (advanced scheduling extends the base scheduler)
