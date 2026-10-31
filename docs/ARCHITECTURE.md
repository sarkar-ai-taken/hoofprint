# Architecture

## Components

```
   CONTROL PLANE (anywhere, no inbound access to customer infra)
                     ┌────────────────────┐
   git events ──────▶│  hoofprint-api     │◀──── hoofprint UI (React)
   webhooks          │  (Go, Postgres)    │◀──── hoofprint CLI
                     │  + hoofprint-verify│
                     └─────────▲──────────┘
                               │ outbound gRPC stream (agent-initiated, TLS)
   ════════════════════════════╪═══════════════════════════ network boundary
                               │
   CUSTOMER CLUSTER(S)         │
                     ┌─────────┴──────────┐
                     │  hoofprint-agent   │  one per cluster, tagged
                     └─────────┬──────────┘
              ┌────────────────┼────────────────┐
              ▼                ▼                ▼
      ┌──────────────┐ ┌──────────────┐ ┌──────────────────┐
      │ Tekton       │ │ k8s API +    │ │ Prometheus · OTel│
      │ (CI steps)   │ │ Rollouts/Flagger│ │ Loki · k8s events│
      └──────────────┘ └──────────────┘ └──────────────────┘
```

### hoofprint-api (control plane)
- Go. Postgres for state. Single binary + Helm chart.
- Owns: projects, pipelines, runs, environments, deploys, verdicts, users/RBAC, agents.
- Compiles `hoofprint.yaml` into work items (Tekton PipelineRuns, Argo Rollout manifests, verification queries) and dispatches them to agents by tag.
- Holds no cluster credentials. Relays agent log streams to the UI over WebSocket.

### hoofprint-agent (delegate)
- Go, static binary, Helm chart. One per cluster. See ADR-0004.
- Outbound-only gRPC stream to the control plane. Pulls work, executes against local k8s API / Rollouts / Flagger / Tekton / Prometheus, streams results back.
- Workload watcher: informers on Deployments, StatefulSets, DaemonSets, Rollouts, Flagger Canaries. Emits a `DeployEvent` on pod-template change with the detected tier.
- Capabilities advertised at registration; control plane routes work accordingly.

### hoofprint-verify
- Runs alongside the control plane; metric and trace queries are dispatched to the agent as work items, results come back as evidence.
- Tier 1: Mann-Whitney U / Kayenta-style canary judgement over Prometheus queries declared in the pipeline spec.
- Tier 2: agent loop. Inputs: git diff, Tekton logs, Rollout status, metric deltas, trace samples. Output: `{verdict: pass|fail|unsure, confidence, rationale, evidence[]}`. Model-agnostic (OpenAI-compatible endpoint, Anthropic, local).
- Every verdict persisted with its evidence bundle for audit.

### UI
- React + TypeScript, Vite. Talks only to hoofprint-api.

### CLI
- Go, same module as api. Thin client over the API.

## Key design rules
1. Hoofprint never fights the CD tool. In tiers 1–2 it observes and, where allowed, acts through that tool's own control points (Rollout pause/abort, Flagger, `rollout undo`). In v1 desired state goes to Git or the Argo API; Argo reconciles.
2. Everything in `hoofprint.yaml` must be expressible without the UI. UI is a view, not a source of truth.
3. Verification sources and the model backend are plugins behind interfaces from day one.
4. One umbrella Helm install for the quickstart (control plane + agent in one cluster). No "install these six things first."
5. The control plane never connects into customer infrastructure. Agents connect out.

## Repo layout (monorepo)
```
hoofprint/
  cmd/api  cmd/agent  cmd/cli
  internal/{pipeline,deploy,verify,store,auth,agentproto}
  proto/   (gRPC definitions)
  ui/
  charts/hoofprint/
  examples/sample-app/
  docs/
```
