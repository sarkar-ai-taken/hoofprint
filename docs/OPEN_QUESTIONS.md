# Open questions

Decide these before or during M0. Move resolved ones to an ADR.

1. **Tekton vs Drone fork for the runner.** Tekton = CNCF, k8s-native, heavy. Drone = simpler, Harness owns the trademark and the recent code is BSL-ish. Leaning Tekton. → ADR-0001
2. **Own UI vs embed Argo's UI.** Own. Single-pane is the whole pitch. → ADR-0002
3. **License.** Apache-2.0 vs AGPL vs BSL. Apache maximizes adoption; AGPL protects against a cloud wrapping it. → ADR-0003
4. **Does the agent verifier ship as part of core or as the first paid feature?** Core. It's the reason to try Hoofprint at all. Paid tier = hosted model, SSO, audit.
5. **Non-k8s targets ever?** Not v1. Revisit if 3+ early users ask.
6. **Name/trademark.** `hoofprint` GitHub org, hoofprint.dev / .io availability — check and register before launch.
7. **Build vs partner for Tier 1 statistics.** Kayenta is Java and dormant; reimplement the judge in Go rather than depend on it.
8. **Agent protocol transport.** gRPC bidi stream vs long-poll HTTP. gRPC is cleaner; long-poll survives hostile corporate proxies. Start gRPC, keep the transport behind an interface.
9. **Sample app.** Needs a realistic microservice with Prometheus metrics and a knob to inject latency/errors. Candidates: fork of Google's microservices-demo, or a 2-service Go app we own.
