# Hoofprint

**The deploy gate for code nobody read.**

Agents write code faster than humans can review it. Hoofprint sits between merge and production: it watches the canary, reads the diff, correlates logs, traces, and metrics, writes a verdict a human can understand, and rolls back on its own when the change is bad.

Open source. Works on top of the Argo CD you already run. Grows into a full delivery platform when you want one.

## The problem

PR volume is up 5–10× on teams using coding agents. Most of that code has had at most a skim from a human. Metrics-threshold canary analysis (Harness CV, Argo Rollouts analysis, Flagger) answers "did p99 go up?" but not "is this change wrong, and why?" When something breaks at 3am, nobody on call understands the diff.

## What Hoofprint does

1. **Watches** every Argo Rollout canary in your cluster via a lightweight agent.
2. **Gathers evidence**: the git diff, deploy logs, metric deltas vs baseline, trace samples, k8s events.
3. **Judges**: statistical canary comparison first, then an LLM verdict with a written rationale and evidence links.
4. **Acts**: pauses promotion, pings the author with the rationale, and rolls back automatically when confidence is high.

## Positioning

| | Harness CV / Flagger / Argo analysis | Hoofprint |
|---|---|---|
| Input | metric thresholds | diff + logs + traces + metrics |
| Output | pass/fail | verdict + rationale + evidence |
| Install | replace your CD | sits on your existing Argo |
| License | commercial / OSS | Apache-2.0 |

## Roadmap in one line

v0: verification gate on existing Argo → v1: full pipelines + deploys with the same agent → v2: governance, Harness import parity.

## Docs

- [Product spec](docs/SPEC.md)
- [Architecture](docs/ARCHITECTURE.md)
- [Roadmap](docs/ROADMAP.md)
- [Harness migration track](docs/MIGRATION.md)
- [Decision records](docs/adr/)
- [Open questions](docs/OPEN_QUESTIONS.md)

## License

Apache-2.0 (proposed). See ADR-0003.
