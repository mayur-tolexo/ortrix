# Security

## Overview

Flowd implements a defense-in-depth security model. Every component authenticates, every connection is encrypted, and authorization is enforced at the capability level.

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

All gRPC connections in Flowd use **mutual TLS**. Both sides of every connection present and validate certificates.

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
                │  Flowd Pods   │
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
  cert_file: /etc/flowd/tls/tls.crt
  key_file: /etc/flowd/tls/tls.key
  ca_file: /etc/flowd/tls/ca.crt
  min_version: "1.3"           # TLS 1.3 minimum
  client_auth: "require"       # Mutual TLS enforced
```

## Service Identity

Every Flowd component has a cryptographic identity derived from its X.509 certificate.

### Identity Format

```
  Subject: CN=gateway.flowd.svc.cluster.local
  SAN:     DNS:gateway.flowd.svc.cluster.local
           DNS:gateway.flowd.svc
           DNS:gateway
```

Identities follow Kubernetes service DNS conventions:

| Component      | Identity                                       |
|---------------|------------------------------------------------|
| Gateway        | `gateway.flowd.svc.cluster.local`              |
| Orchestrator   | `orchestrator-N.flowd.svc.cluster.local`       |
| Worker Service | `<service-name>.<namespace>.svc.cluster.local` |

### SPIFFE Integration (Optional)

For environments using SPIFFE/SPIRE, Flowd supports SPIFFE IDs:

```
  spiffe://cluster.local/ns/flowd/sa/orchestrator
  spiffe://cluster.local/ns/payments/sa/payment-service
```

SPIFFE provides automatic identity bootstrapping and rotation without manual certificate management.

## Gateway as Security Boundary

The gateway is the **only component exposed** to external traffic. It acts as the security perimeter for the Flowd cluster.

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
      app: flowd-orchestrator
  ingress:
    - from:
        - podSelector:
            matchLabels:
              app: flowd-gateway
        - podSelector:
            matchLabels:
              flowd.io/worker: "true"
        - podSelector:
            matchLabels:
              app: flowd-orchestrator
      ports:
        - port: 9090
          protocol: TCP
```

## Capability-Level Authorization

Authorization in Flowd is enforced at the **capability level** — a service can only execute tasks for capabilities it is explicitly allowed to handle.

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
