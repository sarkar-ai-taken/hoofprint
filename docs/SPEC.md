# Hoofprint v1 — Product Spec

## One-liner
An open-source Harness alternative: a single pipeline model and UI over open components, with agent-driven deploy verification.

## Target user
Platform / DevOps engineer at a 20–500 engineer company running Kubernetes, currently gluing together Argo CD + GitHub Actions + Grafana by hand, who has looked at Harness and balked at the price or the lock-in.

## Jobs to be done
1. Define a pipeline once (build → test → deploy → verify) in one YAML file, run it on any Git event.
2. Deploy to Kubernetes with a canary or blue/green strategy without writing Argo Rollouts manifests by hand.
3. Know, automatically, whether a deploy is healthy, and roll back when it isn't, without hand-tuning thresholds.
4. See every pipeline, environment, and deploy in one place.

## v1 scope (in)
### Pipeline engine
- Single YAML spec (`hoofprint.yaml`) covering stages, steps, triggers, environments.
- Steps run as containers. Any OCI image is a step; plugin ecosystem = container images (Drone model).
- Triggers: push, PR, tag, manual, cron, webhook.
- Secrets: Kubernetes secrets + external (Vault, AWS SM) via reference.
- Runner: wrap Tekton (preferred, see ADR-0001) or fork Drone.

### Deployment
- Kubernetes only.
- Strategies: rolling, canary (traffic-weighted), blue/green.
- GitOps sync delegated to Argo CD; Hoofprint writes the desired state, Argo reconciles.
- Environments as first-class objects (dev / staging / prod), with promotion between them.

### Verification (the differentiator)
- Pluggable sources: Prometheus, OpenTelemetry traces, structured logs (Loki), Kubernetes events.
- Tier 1 (ship first): statistical canary vs baseline comparison, Kayenta-style. Auto-rollback on regression.
- Tier 2: LLM agent that reads the diff, the deploy logs, the metric deltas, and the traces, and produces a verdict with a written rationale. Human-in-the-loop by default; auto-rollback opt-in.
- Verdicts are stored, auditable, and shown in the UI next to the deploy.

### Delegate
- `hoofprint-agent` runs in each target cluster, connects outbound to the control plane. Control plane needs no inbound access. See ADR-0004.
- Agents tagged and selected per environment.
- UI shows agent fleet: online/offline, version, last heartbeat.

### UI
- Agent fleet view.
- Pipelines list + run detail with live logs.
- Environments view: what version is where, promotion buttons.
- Deploy detail: strategy progress, verification verdict, rollback button.
- Auth: OIDC. RBAC: project-level, 3 roles (viewer, developer, admin).

### CLI
- `hoofprint init` scaffold, `hoofprint run`, `hoofprint deploy`, `hoofprint status`, `hoofprint rollback`.

## v1 scope (out)
Feature flags, cloud cost management, chaos engineering, non-Kubernetes targets (VMs, serverless, ECS), SaaS hosted offering, multi-tenancy, marketplace.

## Non-goals
- Replacing Argo CD or Tekton. We build on them, we don't fork them unless forced.
- Being a Git host.

## Success criteria for v1
- A new user goes from `git clone` of the sample repo to a verified canary deploy on a kind cluster in under 30 minutes.
- Verification catches an injected latency regression in the sample app and rolls back without human input.
- 3 external teams running it on a real cluster.

## Business model (open core)
- Core: Apache-2.0, everything above.
- Enterprise (later): SSO/SCIM, audit log export, policy-as-code gates, SLA support, hosted control plane.
