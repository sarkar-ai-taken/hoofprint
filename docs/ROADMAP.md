# Roadmap

Solo-dev estimates. Each milestone ends in something runnable on a kind cluster.

## M0 — Foundation (weeks 1–2)
- [ ] Repo scaffold, Go module, Helm chart skeleton, kind dev script
- [ ] `hoofprint.yaml` schema v0 + parser + validator
- [ ] Postgres schema: projects, pipelines, runs
- [ ] ADRs 0001–0003 accepted

## M1 — Pipelines run (weeks 3–5)
- [ ] Compile spec → Tekton PipelineRun
- [ ] GitHub webhook trigger (push, PR)
- [ ] Log streaming API
- [ ] CLI: `init`, `run`, `status`
- [ ] UI: pipelines list, run detail with live logs
- **Demo:** push to sample repo → build + test runs → logs in UI

## M2 — Deploys (weeks 6–8)
- [ ] Environments model + promotion
- [ ] Compile deploy stage → Argo Rollout (rolling, canary, blue/green)
- [ ] Argo CD app bootstrap per environment
- [ ] UI: environments view, deploy detail
- [ ] CLI: `deploy`, `rollback`
- **Demo:** canary deploy of sample app, promoted to prod via UI

## M3 — Verification tier 1 (weeks 9–11)
- [ ] hoofprint-verify service, Prometheus source
- [ ] Canary vs baseline statistical judgement
- [ ] Auto-rollback wiring to Argo Rollouts
- [ ] Verdict storage + UI panel
- **Demo:** inject latency into canary → auto-rollback, verdict visible

## M4 — Verification tier 2, agent (weeks 12–15)
- [ ] Evidence bundle builder (diff, logs, metrics, traces)
- [ ] Agent loop with pluggable model backend
- [ ] Human-approve flow in UI; opt-in auto-rollback
- [ ] Eval set: 20 synthetic regressions, measure precision/recall
- **Demo:** agent explains *why* a deploy is bad, in prose, with evidence links

## M5 — Polish + launch (weeks 16–18)
- [ ] OIDC auth, RBAC
- [ ] Docs site, 30-minute quickstart
- [ ] Sample repo, screencast
- [ ] HN / r/devops / CNCF Slack launch
- **Target:** 3 external teams on a real cluster

## Later
Feature flags · cost · chaos · VM/serverless targets · hosted control plane · enterprise tier
