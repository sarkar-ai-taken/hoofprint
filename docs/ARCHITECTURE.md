# Architecture

## Components

```
                     ┌────────────────────┐
   git events ──────▶│  hoofprint-api     │◀──── hoofprint UI (React)
   webhooks          │  (Go, Postgres)    │◀──── hoofprint CLI
                     └─────────┬──────────┘
                               │ translates hoofprint.yaml
              ┌────────────────┼────────────────┐
              ▼                ▼                ▼
      ┌──────────────┐ ┌──────────────┐ ┌──────────────────┐
      │ Tekton       │ │ Argo CD +    │ │ hoofprint-verify │
      │ (CI steps)   │ │ Argo Rollouts│ │ (verdict engine) │
      └──────────────┘ └──────────────┘ └────────┬─────────┘
                                                 │ reads
                                   Prometheus · OTel · Loki · k8s events · git diff
```

### hoofprint-api (control plane)
- Go. Postgres for state. Single binary + Helm chart.
- Owns: projects, pipelines, runs, environments, deploys, verdicts, users/RBAC.
- Compiles `hoofprint.yaml` into Tekton PipelineRuns and Argo Rollout manifests.
- Streams logs from Tekton pods over WebSocket to the UI.

### hoofprint-verify
- Separate service so it can scale and be swapped independently.
- Tier 1: Mann-Whitney U / Kayenta-style canary judgement over Prometheus queries declared in the pipeline spec.
- Tier 2: agent loop. Inputs: git diff, Tekton logs, Rollout status, metric deltas, trace samples. Output: `{verdict: pass|fail|unsure, confidence, rationale, evidence[]}`. Model-agnostic (OpenAI-compatible endpoint, Anthropic, local).
- Every verdict persisted with its evidence bundle for audit.

### UI
- React + TypeScript, Vite. Talks only to hoofprint-api.

### CLI
- Go, same module as api. Thin client over the API.

## Key design rules
1. Hoofprint never bypasses Argo. Desired state goes to Git or the Argo API; Argo reconciles.
2. Everything in `hoofprint.yaml` must be expressible without the UI. UI is a view, not a source of truth.
3. Verification sources and the model backend are plugins behind interfaces from day one.
4. One Helm install. No "install these six things first."

## Repo layout (monorepo)
```
hoofprint/
  cmd/api  cmd/cli  cmd/verify
  internal/{pipeline,deploy,verify,store,auth}
  ui/
  charts/hoofprint/
  examples/sample-app/
  docs/
```
