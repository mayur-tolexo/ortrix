# Proposal: Gateway Control Plane

## Table of Contents

- [Status](#status)
- [Problem Statement](#problem-statement)
- [Motivation](#motivation)
- [Design Details](#design-details)
  - [Bootstrap Flow](#bootstrap-flow)
  - [Authentication (mTLS)](#authentication-mtls)
  - [Metadata Routing](#metadata-routing)
- [Alternatives Considered](#alternatives-considered)
- [Tradeoffs](#tradeoffs)
- [Testing Strategy](#testing-strategy)
- [Rollout Plan](#rollout-plan)

---

## Status

**Proposed**

---

## Problem Statement

The Ortrix Gateway serves as the control plane — handling bootstrap, authentication, and routing metadata for the cluster. It is the only component exposed to external traffic and acts as the security perimeter. The gateway must be designed to be lightweight (not in the data plane hot path), secure (mTLS everywhere), and reliable (no single point of failure).

---

## Motivation

- **Security boundary**: The gateway is the single entry point for external traffic. It must enforce authentication, authorization, and rate limiting to protect internal components.
- **Operational simplicity**: Clients interact with a single endpoint rather than discovering and connecting to individual orchestrators.
- **Routing abstraction**: Clients do not need to know about partitioning or orchestrator topology. The gateway resolves `workflow_id → partition → orchestrator` transparently.
- **Decoupled scaling**: The gateway scales independently from orchestrators and workers.

---

## Design Details

### Bootstrap Flow

When a client or worker service first connects to the Ortrix cluster, the gateway handles initial bootstrapping:

```
┌──────────┐                    ┌──────────┐                    ┌──────────────┐
│  Client   │                    │ Gateway   │                    │ Orchestrator  │
└─────┬────┘                    └─────┬────┘                    └──────┬───────┘
      │                               │                                │
      │──TLS Handshake───────────────▶│                                │
      │◀──Certificate Exchange───────│                                │
      │                               │                                │
      │──SubmitTask(workflow_id)─────▶│                                │
      │                               │──hash(workflow_id) → partition │
      │                               │──lookup(partition) → owner────▶│
      │                               │◀──owner address──────────────│
      │                               │                                │
      │                               │──forward task────────────────▶│
      │◀──task_id─────────────────────│◀──ack──────────────────────│
      │                               │                                │
```

**Bootstrap sequence for workers:**

1. Worker SDK connects to gateway with mTLS
2. Gateway validates worker certificate against CA
3. Gateway returns orchestrator address for the worker to establish a direct stream
4. Worker opens bidirectional gRPC stream to orchestrator (bypassing gateway for all subsequent communication)

**Bootstrap sequence for clients:**

1. Client connects to gateway with mTLS (or token-based auth)
2. Client submits task with `workflow_id`
3. Gateway computes `partition = hash(workflow_id) % num_partitions`
4. Gateway resolves partition owner from the partition table
5. Gateway forwards task to the owning orchestrator
6. Orchestrator acknowledges; gateway returns task ID to client

**Gateway discovery:**

- In Kubernetes: Gateway is exposed via a `ClusterIP` Service (internal) or `LoadBalancer`/`Ingress` (external)
- DNS-based: `gateway.ortrix.svc.cluster.local`
- Static configuration: Configurable gateway address for non-Kubernetes environments

### Authentication (mTLS)

All connections to and through the gateway use mutual TLS:

**Certificate requirements:**

| Connection               | Client Certificate      | Server Certificate     |
|-------------------------|-------------------------|------------------------|
| External → Gateway       | Client/Service cert     | Gateway cert           |
| Gateway → Orchestrator   | Gateway cert            | Orchestrator cert      |
| Worker → Orchestrator    | Service cert            | Orchestrator cert      |

**TLS configuration:**

```yaml
gateway:
  tls:
    enabled: true
    cert_file: /etc/ortrix/tls/tls.crt
    key_file: /etc/ortrix/tls/tls.key
    ca_file: /etc/ortrix/tls/ca.crt
    min_version: "1.3"
    client_auth: "require"
```

**Identity extraction:**

- Service identity is extracted from the X.509 certificate Subject or SAN
- Identity format: `<service>.<namespace>.svc.cluster.local`
- SPIFFE identity supported optionally: `spiffe://cluster.local/ns/<ns>/sa/<sa>`

**Token-based auth (optional, for external clients):**

- JWT bearer tokens validated against a configured JWKS endpoint
- Tokens carry claims: `sub` (client identity), `scope` (allowed operations), `exp` (expiry)
- Token auth is layered on top of TLS (not a replacement for transport encryption)

**Certificate rotation:**

- Certificates are issued by cert-manager (or equivalent CA)
- Mounted as Kubernetes Secrets into pods
- Gateway watches for file changes and reloads certificates without restart
- Short-lived certificates (24h) to limit exposure window

### Metadata Routing

The gateway maintains a **partition routing table** that maps partitions to their owning orchestrators:

```
┌───────────┬──────────────────┬──────────────────────┐
│ Partition │ Owner            │ Address              │
├───────────┼──────────────────┼──────────────────────┤
│ 0         │ orchestrator-0   │ 10.0.1.5:9090        │
│ 1         │ orchestrator-0   │ 10.0.1.5:9090        │
│ 2         │ orchestrator-1   │ 10.0.2.8:9090        │
│ 3         │ orchestrator-1   │ 10.0.2.8:9090        │
│ ...       │ ...              │ ...                  │
└───────────┴──────────────────┴──────────────────────┘
```

**Routing table sources:**

| Source                | Mechanism                                  |
|----------------------|--------------------------------------------|
| Kubernetes leases     | Watch lease objects for partition ownership |
| etcd                  | Watch keys under `/ortrix/partitions/`     |
| Orchestrator heartbeat| Orchestrators report their owned partitions |

**Routing flow:**

```
1. Client submits task with workflow_id
2. Gateway computes: partition = hash(workflow_id) % num_partitions
3. Gateway looks up: owner = routing_table[partition]
4. Gateway forwards request to owner's address
5. If owner is unreachable:
   a. Refresh routing table
   b. Retry with new owner
   c. Return error after max retries
```

**Routing table consistency:**

- The routing table is eventually consistent (partition ownership changes propagate with bounded delay)
- Stale routing is handled by the orchestrator: if a request arrives at the wrong orchestrator, it returns `PARTITION_NOT_OWNED` and the gateway re-routes
- Cache TTL: routing entries are refreshed every 5s or on miss

**Metadata the gateway does NOT store:**

- Task state (owned by orchestrators)
- Worker registration (owned by orchestrators)
- WAL data (owned by orchestrators)

The gateway is stateless except for the routing table cache, making it trivially horizontally scalable.

---

## Alternatives Considered

| Alternative                   | Pros                           | Cons                                        | Why Not                                     |
|------------------------------|-------------------------------|---------------------------------------------|---------------------------------------------|
| Client-side routing           | No gateway needed              | Clients must understand partitioning         | Leaks internal topology to clients           |
| Service mesh (Istio/Envoy)    | Infrastructure-level routing   | Too generic, no partition awareness          | Cannot route by workflow_id hash             |
| Gateway in data plane          | Simpler topology               | Adds latency to every task dispatch          | Violates low-latency design goal             |

---

## Tradeoffs

- **Gateway as bottleneck vs simplicity**: Routing through the gateway adds a network hop for task submission. The tradeoff is operational simplicity (single entry point) vs an extra ~1ms of latency on submission only (not on the streaming data plane).
- **Stateless gateway vs caching**: A fully stateless gateway would re-resolve routing on every request. Caching reduces latency but introduces staleness.
- **mTLS vs token auth**: mTLS provides stronger guarantees (mutual authentication, encrypted transport) but is harder to manage outside Kubernetes. Token auth is simpler for external clients but provides weaker identity guarantees.
- **Single gateway vs distributed**: A single gateway instance is simpler but is a SPOF. Multiple gateway replicas behind a load balancer provide HA but require consistent routing table views.

---

## Testing Strategy

- **Unit tests**: Test partition hash computation, routing table lookup, certificate validation, token parsing
- **Integration tests**: Full bootstrap flow (client → gateway → orchestrator), verify routing correctness after partition migration
- **Security tests**: Reject connections with invalid certificates, reject expired tokens, verify mTLS enforcement
- **Failure tests**: Gateway crash (verify clients reconnect), orchestrator crash (verify routing table update and re-routing), stale routing table (verify `PARTITION_NOT_OWNED` handling)
- **Load tests**: Measure gateway throughput (requests/sec), verify horizontal scaling with multiple replicas

---

## Rollout Plan

1. **Phase 1**: Implement basic gateway with mTLS, partition hash routing, and task forwarding to orchestrators. Single-replica deployment for development.
2. **Phase 2**: Add routing table caching with watch-based updates from Kubernetes leases. Add `PARTITION_NOT_OWNED` retry handling for stale routes.
3. **Phase 3**: Add token-based authentication for external clients. Add rate limiting and request validation. Deploy as multi-replica StatefulSet or Deployment with health checks.
