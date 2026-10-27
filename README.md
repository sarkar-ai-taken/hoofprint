# Hoofprint

**Open-source software delivery platform.** One pipeline model, one UI, from commit to verified production deploy.

Hoofprint stitches the best open-source delivery components into a single opinionated platform, and adds the thing none of them have: a verification layer that reasons about a deploy, not just thresholds it.

## Why

Harness.io sells integration, not components. Every piece of a modern delivery platform already exists in open source, but nobody has made them feel like one product:

| Capability        | Harness module         | Hoofprint builds on       |
|-------------------|------------------------|---------------------------|
| CI                | Harness CI (Drone)     | Tekton or Drone fork      |
| CD / GitOps       | Harness CD             | Argo CD + Argo Rollouts   |
| Verification      | Continuous Verification| Prometheus/OTel + LLM agent (the moat) |
| UI                | Harness Platform       | Hoofprint UI              |
| Feature flags     | Harness FF             | deferred (Flagsmith)      |
| Cost              | Harness CCM            | deferred (OpenCost)       |
| Chaos             | Harness CE             | deferred (Litmus)         |

## Status

Planning. See [`docs/`](docs/) for the spec, architecture, roadmap, and decision log.

## Docs

- [Product spec (v1)](docs/SPEC.md)
- [Architecture](docs/ARCHITECTURE.md)
- [Roadmap](docs/ROADMAP.md)
- [Decision records](docs/adr/)
- [Open questions](docs/OPEN_QUESTIONS.md)

## License

Apache-2.0 (proposed). See ADR-0003.
