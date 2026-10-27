# ADR-0004: Delegate / agent architecture

**Status:** Proposed (v2)

## Context
Harness's delegate is a lightweight agent run inside the customer's network. It polls the control plane outbound, so the control plane never needs inbound access to customer infrastructure. This is why Harness sells into regulated environments, and any serious replacement needs the same shape.

## Decision
Design hoofprint-api from day one so execution is decoupled from the control plane via an outbound-only agent protocol, even though v1 ships with the agent and control plane co-located in one cluster.

Concretely:
- All execution (Tekton runs, Argo sync, verification queries) goes through an `Executor` interface.
- v1 implements `LocalExecutor` (same cluster).
- v2 implements `AgentExecutor`: a `hoofprint-agent` binary that long-polls or holds a gRPC stream to the control plane, pulls work, runs it in its cluster, streams results back.

## Why now
Retrofitting an agent model onto a control plane that assumes direct cluster access is a rewrite. Putting the interface in now costs almost nothing.

## Consequences
- Slight indirection in v1 code.
- Enables the hosted control plane business model later without re-architecting.
