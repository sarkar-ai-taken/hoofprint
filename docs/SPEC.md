# Hoofprint v1 — Product Spec

## One-liner
The deploy gate for code nobody read: agent-driven verification of every canary, on top of the Argo CD you already run, growing into a full open-source delivery platform.

## Target user
Platform / DevOps engineer at a 20–500 engineer company on Kubernetes + Argo, whose team has adopted coding agents and whose PR volume now outruns human review. Secondary: the same person evaluating Harness and balking at price or lock-in.

## Jobs to be done
1. Know, for every deploy, whether the change is bad and *why*, in language the on-call can act on, without hand-tuning thresholds.
2. Roll back automatically when the evidence is strong, pause and ask a human when it isn't.
3. Adopt this without replacing the CD system already in place.
4. (v1) Define a pipeline once, deploy with canary/blue-green, see everything in one place.

## Why now
Coding agents have multiplied PR volume while review depth has fallen. Existing canary analysis (Harness CV, Flagger, Argo analysis templates) compares metrics to thresholds and says pass/fail; none of it reads the diff or explains itself. The gate between merge and production is the new bottleneck.

## v0 scope (in) — ships first
### Verification gate
- Agent watches Argo Rollouts in the cluster; no Hoofprint pipelines required.
- Evidence: git diff, pod logs, k8s events, Prometheus canary-vs-baseline, OTel trace samples.
- Tier 1 judge: statistical canary comparison, in Go.
- Tier 2 judge: LLM verdict `{pass|fail|unsure, confidence, rationale, evidence[]}`, model backend pluggable.
- Actions: pause rollout, comment on PR, Slack, auto-abort (opt-in, confidence-gated).
- Verdicts and evidence bundles persisted and auditable.
- Delegate architecture from day one (ADR-0004).
- Minimal UI: rollouts, verdicts, agent fleet. OIDC + project RBAC.

## v1 scope (in) — delivery platform
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

## Success criteria for v0
- Install on an existing Argo cluster in under 20 minutes, no pipeline changes.
- On the eval set of 30 synthetic regressions: ≥90% recall, ≥80% precision, verdict within 5 minutes of canary start.
- Rationale names the offending diff hunk in ≥70% of logic/config regressions.
- 5 external teams running it; one recorded real catch.

## Success criteria for v1
- Clone-to-verified-canary on kind in under 30 minutes.
- 3 teams using Hoofprint pipelines, not just the gate.

## Business model (open core)
- Core: Apache-2.0, everything above, including the LLM judge (bring your own model key).
- Paid (later): hosted control plane with managed model, SSO/SCIM, audit export, policy gates, SLA support.
