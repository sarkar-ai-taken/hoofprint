# Roadmap

Solo-dev estimates. Re-cut around verification as the product; the delivery platform follows.

## v0 — Verification gate on existing Argo

### M0 — Foundation (weeks 1–2)
- [ ] Repo scaffold, Go module, Helm chart skeleton, kind dev script with Argo CD + Rollouts + Prometheus preinstalled
- [ ] Postgres schema: clusters, agents, rollouts, verdicts, evidence
- [ ] gRPC agent protocol: register, heartbeat, work stream (ADR-0004)
- [ ] `hoofprint-agent` skeleton: connects, registers, lists Argo Rollouts in its cluster
- [ ] Sample app with injectable latency/error/memory regressions

### M1 — Evidence + statistical verdict (weeks 3–6)
- [ ] Agent watches Rollout canary steps, emits `RolloutStarted/Paused/Promoted/Aborted`
- [ ] Evidence collectors on agent: Prometheus (canary vs baseline), k8s events, pod logs (Loki or direct), OTel trace samples
- [ ] Git diff fetch (GitHub app) for the commit range in the rollout
- [ ] Tier 1 judge: Mann-Whitney canary comparison over declared metrics, in Go
- [ ] Verdict stored with evidence bundle
- [ ] CLI: `hoofprint verdicts`, `hoofprint explain <rollout>`
- **Demo:** inject latency → statistical fail → verdict visible in CLI

### M2 — LLM verdict + action (weeks 7–10)
- [ ] Evidence bundle → prompt builder, pluggable model backend (Anthropic, OpenAI-compatible, local)
- [ ] Verdict: `{pass|fail|unsure, confidence, rationale, evidence[]}`
- [ ] Actions: pause Rollout, post rationale to PR + Slack, auto-abort when confidence ≥ threshold (opt-in)
- [ ] Eval set: 30 synthetic regressions across latency/errors/memory/config/logic; measure precision, recall, time-to-verdict
- [ ] UI v0: rollouts list, verdict detail with evidence, agent fleet
- **Demo:** agent-written PR with a subtle bug → canary → Hoofprint explains the bug in prose, links the diff line, rolls back

### M3 — Launch v0 (weeks 11–12)
- [ ] OIDC auth, project-level RBAC
- [ ] Docs, 20-minute quickstart on kind, screencast
- [ ] Launch: HN, r/devops, Argo community, CNCF Slack
- **Target:** 5 teams running it against real Argo installs; 1 recorded "it caught something" story

## v1 — Delivery platform (weeks 13–26)
Same agent, now executes work too.
- Pipeline spec `hoofprint.yaml` → Tekton work items (ADR-0001)
- Deploy stages → Argo Rollouts manifests, environments + promotion
- Full UI (ADR-0002), CLI `run`/`deploy`/`rollback`
- `hoofprint import harness` for CI + k8s deploy stages (MIGRATION.md)
- Verification gate runs on Hoofprint-managed deploys and standalone Argo alike

## v2 — Governance + Harness enterprise parity
Approvals (Jira/ServiceNow) · templates · OPA policy gates · audit log · Helm/ECS/Lambda/SSH/Terraform targets (same agent binary, new capabilities)

## Later
Feature flags · cost · chaos · hosted control plane · enterprise tier
