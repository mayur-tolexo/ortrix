# Security


## Table of Contents

- [Overview](#overview)
- [mTLS (Mutual TLS)](#mtls-mutual-tls)
  - [Connections Secured by mTLS](#connections-secured-by-mtls)
  - [Certificate Management](#certificate-management)
  - [TLS Configuration](#tls-configuration)
- [Service Identity](#service-identity)
  - [Identity Format](#identity-format)
  - [SPIFFE Integration (Optional)](#spiffe-integration-optional)
- [Gateway as Security Boundary](#gateway-as-security-boundary)
  - [Gateway Security Functions](#gateway-security-functions)
  - [Network Policies](#network-policies)
- [Capability-Level Authorization](#capability-level-authorization)
  - [Authorization Model](#authorization-model)
  - [Enforcement Points](#enforcement-points)
  - [Capability Policy](#capability-policy)
  - [Authorization Flow](#authorization-flow)
  - [Principle of Least Privilege](#principle-of-least-privilege)

---

## Overview

Ortrix implements a defense-in-depth security model. Every component authenticates, every connection is encrypted, and authorization is enforced at the capability level.

```
  ┌──────────────────────────────────────────────────────┐
  │                  Security Layers                     │
  │                                                      │
  │  1. Transport Security    mTLS on all connections    │
  │  2. Identity              X.509 service identity     │
  │  3. Authentication        Certificate validation     │
  │  4. Authorization         Capability-level ACLs      │
  │  5. Gateway Boundary      External traffic filtering │
  └──────────────────────────────────────────────────────┘
```

## mTLS (Mutual TLS)

All gRPC connections in Ortrix use **mutual TLS**. Both sides of every connection present and validate certificates.

### Connections Secured by mTLS

| Connection                  | Client             | Server          |
|-----------------------------|--------------------|-----------------| 
| Client → Gateway            | Client cert        | Gateway cert    |
| Gateway → Orchestrator      | Gateway cert       | Orchestrator cert|
| Worker SDK → Orchestrator   | Service cert       | Orchestrator cert|
| Orchestrator → Orchestrator | Orchestrator cert  | Orchestrator cert|

### Certificate Management

```
  ┌──────────────┐     ┌──────────────┐     ┌──────────────┐
  │   cert-manager│     │  Kubernetes   │     │   Vault      │
  │   (issuer)    │     │  Secrets      │     │  (optional)  │
  └──────┬───────┘     └──────┬───────┘     └──────┬───────┘
         │                     │                     │
         └────── cert distribution ──────────────────┘
                       │
                ┌──────▼───────┐
                │  Ortrix Pods   │
                │  (mounted as  │
                │   volumes)    │
                └──────────────┘
```

In Kubernetes:
- Certificates are issued by **cert-manager** or a similar CA
- Distributed via Kubernetes Secrets, mounted into pods
- Automatic rotation with no downtime (watch for file changes)
- Short-lived certificates (24h) to limit exposure window

### TLS Configuration

```yaml
tls:
  enabled: true
  cert_file: /etc/ortrix/tls/tls.crt
  key_file: /etc/ortrix/tls/tls.key
  ca_file: /etc/ortrix/tls/ca.crt
  min_version: "1.3"           # TLS 1.3 minimum
  client_auth: "require"       # Mutual TLS enforced
```

## Service Identity

Every Ortrix component has a cryptographic identity derived from its X.509 certificate.

### Identity Format

```
  Subject: CN=gateway.ortrix.svc.cluster.local
  SAN:     DNS:gateway.ortrix.svc.cluster.local
           DNS:gateway.ortrix.svc
           DNS:gateway
```

Identities follow Kubernetes service DNS conventions:

| Component      | Identity                                       |
|---------------|------------------------------------------------|
| Gateway        | `gateway.ortrix.svc.cluster.local`              |
| Orchestrator   | `orchestrator-N.ortrix.svc.cluster.local`       |
| Worker Service | `<service-name>.<namespace>.svc.cluster.local` |

### SPIFFE Integration (Optional)

For environments using SPIFFE/SPIRE, Ortrix supports SPIFFE IDs:

```
  spiffe://cluster.local/ns/ortrix/sa/orchestrator
  spiffe://cluster.local/ns/payments/sa/payment-service
```

SPIFFE provides automatic identity bootstrapping and rotation without manual certificate management.

## Gateway as Security Boundary

The gateway is the **only component exposed** to external traffic. It acts as the security perimeter for the Ortrix cluster.

```
  ┌──────────────────────────────────────────────┐
  │            Kubernetes Cluster                 │
  │                                               │
  │  External                    Internal         │
  │  ┌───────┐                                    │
  │  │Ingress│──▶ Gateway ──▶ Orchestrators      │
  │  └───────┘       │                            │
  │                  │        Workers (services)   │
  │                  │                            │
  │           ┌──────▼──────┐                     │
  │           │  Security   │                     │
  │           │  Boundary   │                     │
  │           └─────────────┘                     │
  └──────────────────────────────────────────────┘
```

### Gateway Security Functions

| Function                | Description                                   |
|------------------------|-----------------------------------------------|
| TLS Termination        | External TLS at ingress, mTLS internally      |
| Authentication         | Validate client certificates or tokens        |
| Rate Limiting          | Protect internal components from abuse         |
| Request Validation     | Sanitize and validate incoming requests        |
| Audit Logging          | Log all external API calls                     |

### Network Policies

Kubernetes NetworkPolicies enforce that:

```yaml
# Orchestrators only accept connections from:
#   - Gateway (for task submission)
#   - Workers (for streaming)
#   - Other Orchestrators (for replication)
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: orchestrator-ingress
spec:
  podSelector:
    matchLabels:
      app: ortrix-orchestrator
  ingress:
    - from:
        - podSelector:
            matchLabels:
              app: ortrix-gateway
        - podSelector:
            matchLabels:
              ortrix.io/worker: "true"
        - podSelector:
            matchLabels:
              app: ortrix-orchestrator
      ports:
        - port: 9090
          protocol: TCP
```

## Secure Worker Connectivity

Ortrix enforces a strict **outbound-only** connection model for workers. This eliminates the need for workers to expose any listening ports, significantly reducing the attack surface.

### Outbound-Only Connections

Workers always initiate connections to orchestrators. Orchestrators never dial worker pods.

```
  ┌─────────────┐                          ┌───────────────┐
  │   Worker     │                          │ Orchestrator   │
  │  (no open    │──outbound TCP/gRPC────▶│  (listens on   │
  │   ports)     │                          │   port 9090)   │
  │              │◀═══bidirectional═══════│                │
  │              │     stream              │                │
  └─────────────┘                          └───────────────┘

  ✓ Worker initiates connection (outbound only)
  ✗ Orchestrator does NOT connect to worker
  ✗ No worker ports are exposed
```

This model ensures that:
- Workers have **zero inbound network exposure** for orchestration traffic
- Compromising an orchestrator cannot be used to directly connect to worker pods
- Workers behind NAT, service meshes, or restricted network segments can still participate

### mTLS Authentication

Every worker-to-orchestrator connection is secured with **mutual TLS** (mTLS). Both parties present and verify X.509 certificates during the handshake.

```
  Worker                                  Orchestrator
    │                                         │
    │──TLS ClientHello──────────────────────▶│
    │◀──TLS ServerHello + Server Certificate──│
    │   (worker verifies orchestrator)        │
    │──Client Certificate─────────────────▶  │
    │   (orchestrator verifies worker)        │
    │◀──Handshake Complete───────────────── │
    │                                         │
    │══(encrypted bidirectional stream)══════│
```

The orchestrator validates:
1. The worker's certificate is signed by a trusted CA
2. The certificate is not expired or revoked
3. The service identity (SAN/CN) is authorized for the declared capabilities

### Service Identity (SPIFFE-Style)

Each worker has a cryptographic service identity embedded in its X.509 certificate. Ortrix supports both standard Kubernetes DNS identities and SPIFFE IDs:

```
  Standard Kubernetes identity:
    CN=payment-service.payments.svc.cluster.local
    SAN=payment-service.payments.svc.cluster.local

  SPIFFE identity:
    spiffe://cluster.local/ns/payments/sa/payment-service
```

Service identity is used to:
- **Authenticate** the worker during mTLS handshake
- **Authorize** which capabilities the worker can register
- **Audit** which service executed a given task
- **Trace** task execution across services

### No Exposed Worker Ports

Workers using the Ortrix SDK do **not** open any listening ports for orchestration traffic. The SDK operates entirely over an outbound connection:

```
  Traditional worker model (REJECTED):
    Worker listens on :8080 ← orchestrator connects inbound
    → Requires NetworkPolicy to allow inbound traffic
    → Port scanning reveals worker endpoints
    → Each worker is a potential attack target

  Ortrix worker model (CHOSEN):
    Worker connects outbound to orchestrator:9090
    → No inbound ports needed for orchestration
    → Worker is invisible to port scanners
    → Attack surface is limited to the orchestrator endpoint
```

Workers may still expose ports for their own application traffic (HTTP APIs, etc.), but Ortrix orchestration traffic requires zero inbound ports.

### Network Policy Considerations

The outbound-only model simplifies Kubernetes NetworkPolicy configuration:

```yaml
# Workers: allow outbound to orchestrator only
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: worker-egress
spec:
  podSelector:
    matchLabels:
      ortrix.io/worker: "true"
  policyTypes:
    - Egress
  egress:
    - to:
        - podSelector:
            matchLabels:
              app: ortrix-orchestrator
      ports:
        - port: 9090
          protocol: TCP
```

```yaml
# Orchestrators: accept inbound from workers and gateway
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: orchestrator-ingress-secure
spec:
  podSelector:
    matchLabels:
      app: ortrix-orchestrator
  policyTypes:
    - Ingress
  ingress:
    - from:
        - podSelector:
            matchLabels:
              ortrix.io/worker: "true"
        - podSelector:
            matchLabels:
              app: ortrix-gateway
        - podSelector:
            matchLabels:
              app: ortrix-orchestrator
      ports:
        - port: 9090
          protocol: TCP
```

This approach:
- Minimizes the blast radius of a compromised component
- Follows the principle of least privilege at the network level
- Works naturally with Kubernetes namespace isolation
- Is compatible with service mesh policies (Istio, Linkerd)

## Capability-Level Authorization

Authorization in Ortrix is enforced at the **capability level** — a service can only execute tasks for capabilities it is explicitly allowed to handle.

### Authorization Model

```
  Service Identity → Allowed Capabilities → Task Execution

  payment-service.payments.svc  → ["process_payment", "refund_payment"]
  email-service.comms.svc       → ["send_email", "send_sms"]
```

### Enforcement Points

```
  1. Worker Registration
     ┌─────────────┐
     │ Worker SDK   │──Register(capabilities)──▶ Orchestrator
     │              │                            │
     │              │                            ▼
     │              │                    Verify: is this service
     │              │                    allowed these capabilities?
     │              │                            │
     │              │◀──Ack/Reject──────────────│
     └─────────────┘
```

```
  2. Task Dispatch
     Orchestrator verifies that the selected worker
     is authorized for the task type before dispatch.
```

### Capability Policy

```yaml
# Capability authorization policy
capabilities:
  process_payment:
    allowed_services:
      - "payment-service.payments.svc.cluster.local"
      - "payment-service-v2.payments.svc.cluster.local"
  send_email:
    allowed_services:
      - "email-service.comms.svc.cluster.local"
  # Wildcard: any authenticated service
  health_check:
    allowed_services: ["*"]
```

### Authorization Flow

```
1. Worker connects with mTLS → identity extracted from certificate
2. Worker sends WorkerRegistration with capabilities list
3. Orchestrator checks each capability against the policy:
   a. Service identity matches allowed_services → ALLOW
   b. No match → REJECT registration, close stream
4. If allowed, worker is added to capability index
5. On each task dispatch, orchestrator re-verifies authorization
```

### Principle of Least Privilege

- Workers can only receive tasks they declared AND are authorized for
- Gateway cannot dispatch tasks directly — only route to orchestrators
- Orchestrators cannot execute tasks — only dispatch to authorized workers
- Each component has exactly the permissions it needs, no more
