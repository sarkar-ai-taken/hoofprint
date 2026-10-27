# Roadmap

Solo-dev estimates. Delegate model is in v1 (ADR-0004), governance is v2. Each milestone ends in something runnable on a kind cluster.

## M0 — Foundation (weeks 1–3)
- [ ] Repo scaffold, Go module, Helm chart skeleton, kind dev script
- [ ] `hoofprint.yaml` schema v0 + parser + validator
- [ ] Postgres schema: projects, pipelines, runs
- [ ] ADRs 0001–0004 accepted
- [ ] gRPC agent protocol defined (proto), register + heartbeat + work stream
- [ ] `hoofprint-agent` skeleton: connects, registers, heartbeats, shows up in API

## M1 — Pipelines run (weeks 4–7)
- [ ] Compile spec → Tekton PipelineRun work item
- [ ] Agent executes Tekton work items, streams logs back
- [ ] Agent selection by tag
- [ ] GitHub webhook trigger (push, PR)
- [ ] Log streaming API
- [ ] CLI: `init`, `run`, `status`
- [ ] `hoofprint import harness` — CI stages, triggers, connectors (see MIGRATION.md)
- [ ] UI: agent fleet view, pipelines list, run detail with live logs
- **Demo:** push to sample repo → build + test runs → logs in UI

## M2 — Deploys (weeks 8–10)
- [ ] Environments model + promotion
- [ ] Compile deploy stage → Argo Rollout (rolling, canary, blue/green)
- [ ] Argo CD app bootstrap per environment
- [ ] UI: environments view, deploy detail
- [ ] CLI: `deploy`, `rollback`
- [ ] Importer: k8s deploy stages + Service object
- **Demo:** canary deploy of sample app, promoted to prod via UI

## M3 — Verification tier 1 (weeks 11–13)
- [ ] hoofprint-verify, Prometheus queries dispatched to agent as work items
- [ ] Canary vs baseline statistical judgement
- [ ] Auto-rollback wiring to Argo Rollouts
- [ ] Verdict storage + UI panel
- **Demo:** inject latency into canary → auto-rollback, verdict visible

## M4 — Verification tier 2, LLM verdicts (weeks 14–17)
- [ ] Evidence bundle builder (diff, logs, metrics, traces)
- [ ] Agent loop with pluggable model backend
- [ ] Human-approve flow in UI; opt-in auto-rollback
- [ ] Eval set: 20 synthetic regressions, measure precision/recall
- **Demo:** agent explains *why* a deploy is bad, in prose, with evidence links

## M5 — Polish + launch (weeks 18–20)
- [ ] OIDC auth, RBAC
- [ ] Docs site, 30-minute quickstart
- [ ] Sample repo, screencast
- [ ] HN / r/devops / CNCF Slack launch
- **Target:** 3 external teams on a real cluster

## v2 — Governance + Harness enterprise parity
Approvals (Jira/ServiceNow) · templates · OPA policy gates · audit log · Helm/ECS/Lambda/SSH/Terraform targets (same agent binary, new capabilities)

## Later
Feature flags · cost · chaos · VM/serverless targets · hosted control plane · enterprise tier
