# Developing Hoofprint

## Prerequisites

- Go (the version in `go.mod`; `GOTOOLCHAIN=auto` downloads it)
- Docker, kubectl, Helm
- kind: `go install sigs.k8s.io/kind@latest`
- For proto changes: `go install github.com/bufbuild/buf/cmd/buf@latest google.golang.org/protobuf/cmd/protoc-gen-go@latest google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest`

## Layout

```
cmd/api        hoofprint-api: gRPC for agents (:9090), HTTP API (:8080), Postgres or memory store
cmd/agent      hoofprint-agent: outbound stream, capability detection, workload watcher, work handlers
cmd/cli        hoofprint: thin client over the HTTP API
internal/
  agentproto/  generated from proto/ (do not edit)
  model/       domain types: Agent, Deploy, Verdict, Evidence, Tier
  store/       Store interface; postgres/ (prod, embedded migrations), memory/ (dev, tests), storetest/ (conformance suite)
  controlplane/ Registry (sessions, reaper), Dispatcher (Executor over agents), gRPC Server
  api/         HTTP JSON handlers
  agent/       runtime (reconnect, heartbeat, outbox), handlers; capabilities/ (CRD probes); watcher/ (informers -> DeployEvent)
  imageref/    image tag / annotation / label -> git revision
  verify/stats Mann-Whitney U canary judge (tier 1)
proto/         gRPC definitions (buf)
charts/hoofprint  umbrella Helm chart: api + postgres + agent
examples/sample-app  service with injectable latency / errors / memory leak
hack/          kind-up.sh, kind-down.sh
```

## Day-to-day loop

```sh
make test            # unit tests (Postgres suite skips unless HOOFPRINT_TEST_DATABASE_URL is set)
make test-race
make ci              # vet + test + helm lint, what GitHub Actions runs
make proto           # after editing proto/
```

Run the Postgres conformance suite locally:

```sh
docker run -d --rm --name pg -p 55432:5432 -e POSTGRES_PASSWORD=pw postgres:16-alpine
HOOFPRINT_TEST_DATABASE_URL='postgres://postgres:pw@localhost:55432/postgres?sslmode=disable' go test ./internal/store/postgres/
docker rm -f pg
```

## End to end on kind

```sh
make kind-up          # kind + Argo Rollouts + Argo CD + kube-prometheus-stack + sample-app (~5-10 min first time)
make run-api          # terminal 1: control plane, in-memory store, gate on with 60s windows
make prom-forward     # terminal 2: Prometheus on localhost:9091 for the local agent
make run-agent        # terminal 3: agent using your kubeconfig, cluster name kind-hoofprint
go run ./cmd/cli agents
go run ./cmd/cli workloads agent-kind-hoofprint sample
```

Trigger a deploy the way CI would, by changing the pod template, and watch it land:

```sh
kubectl -n sample set env deployment/sample-app INJECT_LATENCY_MS=300 APP_VERSION=v2
go run ./cmd/cli deploys
go run ./cmd/cli deploy <id>
```

Scaling (`kubectl scale`) is deliberately not a deploy; only pod-template changes are.

`SKIP_ARGOCD=1 make kind-up` saves about a minute and 500Mi. `WITH_FLAGGER=1` adds Flagger.

### In-cluster install instead of local processes

```sh
make images VERSION=dev IMAGE_REPO=ghcr.io/sarkar-ai-taken
kind load docker-image ghcr.io/sarkar-ai-taken/hoofprint-api:dev ghcr.io/sarkar-ai-taken/hoofprint-agent:dev --name hoofprint
helm upgrade --install hoofprint charts/hoofprint -n hoofprint --create-namespace -f charts/hoofprint/values-kind.yaml
HOOFPRINT_API=http://localhost:30080 go run ./cmd/cli agents
```

## The demo

`make demo` (after `make kind-up`) runs `hack/demo.sh`: it tags the sample-app image with three fake commit SHAs, resets the sample app, starts the control plane with the gate on short windows (`--verify-window 60s --verify-settle 20s --verify-step 5s`), ships a bad deploy and then a revert, waits for both verdicts, and saves every CLI output under `docs/demo/*.txt`. `hack/screenshots.sh` renders those into `docs/img/*.png` (freeze) and `docs/img/demo.gif` (ffmpeg). Total about 4 minutes.

## The verification gate

`hoofprint-api --verify-config examples/verify-sample-app.yaml` enables it. For each observed deploy in a configured namespace the gate waits `settle + window`, then asks the deploy's agent to run every declared query over two windows, `[observed - window, observed]` and `[observed + settle, observed + settle + window]`, pools the samples, runs `stats.Judge`, and stores a verdict with one `metric` evidence record per metric and one `change` record for the template diff. The agent needs `--prometheus-url` (capability `prometheus`); without it the gate records an `unsure` verdict saying so.

Queries are Go templates over the deploy (`{{.Namespace}}`, `{{.Name}}`, `{{.Labels.app}}`). Make sure a query returns a value even when nothing is wrong: `error_rate` wraps the numerator in `or vector(0)` so a window with no 5xx yields zeros rather than an empty vector, which the judge would otherwise report as "too few samples".

## Protocol in one paragraph

The agent opens one bidirectional gRPC stream (`AgentService.Connect`) with a bearer token in metadata and sends `Register` first. The control plane answers `Registered` with the heartbeat interval, then pushes `WorkItem{id, kind, payload}`; the agent acks, streams `WorkEvent{log|status|result}` back, and sends `DeployEvent`s from its watcher whenever a workload's pod template changes. Silence beyond `--offline-after` (30s default) marks the agent offline and closes the stream; the agent reconnects with backoff and flushes its outbox. See ADR-0004.

## Work kinds (M0)

| kind | payload | result |
|---|---|---|
| `ping` | any JSON | echoed |
| `workloads.list` | `{"namespace": ""}` | `{"workloads": [{namespace, kind, name, replicas, ready, images, revision, labels}]}` |
| `prometheus.query_range` | `{"query", "start", "end", "step"}` | `{"series": [{"metric": {...}, "values": [{"t", "v"}]}]}` (needs `--prometheus-url`) |

## Conventions

- Store implementations must pass `storetest.Run`.
- Nothing in the control plane reaches into a cluster directly; everything goes through `controlplane.Executor` (ADR-0004).
- Server goroutines must not log through `t.Log` in tests; use the package's `testLogger()`.
- Generated code under `internal/agentproto` is committed; CI fails if it drifts from `proto/`.
