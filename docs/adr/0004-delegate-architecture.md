# ADR-0004: Delegate / agent architecture

**Status:** Accepted (v1)

## Context
Harness's delegate is a lightweight agent run inside the customer's network. It polls the control plane outbound, so the control plane never needs inbound access to customer infrastructure. This is why Harness sells into regulated environments, and any serious replacement needs the same shape.

## Decision
Ship the delegate model in v1. Two binaries:

- **hoofprint-api** (control plane): API, Postgres, UI, scheduler. Holds no cluster credentials. Never initiates connections into customer infrastructure.
- **hoofprint-agent** (delegate): runs inside each target cluster. Opens an outbound gRPC stream to the control plane, authenticates with a per-agent token, pulls work items, executes them locally (Tekton runs, Argo sync, Prometheus queries), streams logs and results back.

The control plane *only* talks to agents. All execution goes through the `Executor` interface, implemented by `AgentExecutor`. There is no `LocalExecutor`; the single-cluster quickstart simply runs an agent in the same cluster as the control plane.

### Protocol
- gRPC bidirectional stream, agent-initiated, TLS.
- Agent registers with: id, cluster name, capabilities (tekton, argo, prometheus, …), version.
- Control plane pushes `WorkItem{id, kind, payload}`; agent acks, streams `WorkEvent{log|status|result}`.
- Heartbeat every 10s; agent marked offline after 30s silence; in-flight work re-queued or failed per kind.
- Agent selection by tags (`cluster=prod-us`, `env=staging`), same model as Harness delegate selectors.

### Agent install
`helm install hoofprint-agent --set controlPlane=https://… --set token=…` or a single static binary for VMs later.

## Why v1
- It's the architecture Harness customers already run; matching it removes an objection at import time.
- It's required for the hosted control plane model, which is the most likely commercial path.
- Retrofitting it later is a rewrite. Doing it first is a few weeks.

## Consequences
- M0/M1 grow by roughly two weeks for the protocol and agent binary.
- Quickstart installs two charts (or one umbrella chart). Must still hit the 30-minute target.
- Log streaming goes agent → control plane → UI rather than control plane reading pods directly.
- Opens the door to non-k8s targets later: a VM agent is the same binary with different capabilities.
