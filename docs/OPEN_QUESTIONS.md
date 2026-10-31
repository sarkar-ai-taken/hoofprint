# Open questions

Decide these before or during M0. Move resolved ones to an ADR.

-1. **Metoro.** Closest competitor: Helm install, eBPF, watches any k8s change, Healthy/Regression/Inconclusive to Slack, drafts a rollback PR. Not open source. We differ on: source diff, rationale naming the hunk, autonomous action, Apache-2.0. Track their releases; if they add source diff + action, the moat is OSS + eval quality alone.
0. **Judge quality is the product.** How do we build the eval set? Synthetic injections are a start; need real regressions from early users. Precision matters more than recall early — a false rollback costs trust faster than a miss.
1. **Tekton vs Drone fork for the runner.** Tekton = CNCF, k8s-native, heavy. Drone = simpler, Harness owns the trademark and the recent code is BSL-ish. Leaning Tekton. → ADR-0001
2. **Own UI vs embed Argo's UI.** Own. Single-pane is the whole pitch. → ADR-0002
3. **License.** Apache-2.0 vs AGPL vs BSL. Apache maximizes adoption; AGPL protects against a cloud wrapping it. → ADR-0003
4. **Model cost per verdict.** Evidence bundles can be large. Budget target: < $0.10 per verdict at default settings. Needs trace/log sampling and diff truncation strategy.
5. **Non-k8s targets ever?** Not v1. Revisit if 3+ early users ask.
6. **Name/trademark.** `hoofprint` GitHub org, hoofprint.dev / .io availability — check and register before launch.
7. **Build vs partner for Tier 1 statistics.** Kayenta is Java and dormant; reimplement the judge in Go rather than depend on it.
8. **Agent protocol transport.** gRPC bidi stream vs long-poll HTTP. gRPC is cleaner; long-poll survives hostile corporate proxies. Start gRPC, keep the transport behind an interface.
9. **Image tag → commit SHA.** Tier 1 needs it for the diff. Options: require SHA in tag (common), OCI `org.opencontainers.image.revision` annotation, or a label on the pod template. Support all three, document the first.
10. **Sample app.** Needs a realistic microservice with Prometheus metrics and a knob to inject latency/errors. Candidates: fork of Google's microservices-demo, or a 2-service Go app we own.
