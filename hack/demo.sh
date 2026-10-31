#!/usr/bin/env bash
# Scripted demo against the kind cluster from hack/kind-up.sh.
#
# Runs the control plane and agent locally, ships a bad deploy (300ms of
# injected latency) and then a good one to the sample app, waits for the
# verdicts, and saves every CLI output to docs/demo/*.txt. hack/screenshots.sh
# renders those into docs/img/*.png for the README.
#
#   make demo
#   VERIFY_WINDOW=60s VERIFY_SETTLE=20s hack/demo.sh    (defaults; ~3 min total)
set -euo pipefail
cd "$(dirname "$0")/.."
export GOTOOLCHAIN=auto PATH="$HOME/go/bin:$PATH" HOOFPRINT_API=http://localhost:18080
OUT=docs/demo
LOGS=${DEMO_LOGS:-/tmp/hoofprint-demo}
WINDOW=${VERIFY_WINDOW:-60s}
SETTLE=${VERIFY_SETTLE:-20s}
STEP=${VERIFY_STEP:-5s}
mkdir -p "$OUT" "$LOGS"
kubectl config use-context kind-hoofprint >/dev/null

say() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
capture() { # capture <file> <cmd...>: run, print, and save as "$ cmd" + output
  local f=$1; shift
  { printf '$ %s\n' "$*"; "$@"; } | tee "$OUT/$f.txt"
}

say "building"
make -s build

# Three "commits" of the sample app. Same binary; the regression is injected
# through env so the diff is small and realistic.
GOOD_SHA=3140f8e   # baseline
BAD_SHA=9b1d3a7    # the agent-written change that adds 300ms
FIX_SHA=c47e2f0    # the revert
say "tagging sample-app images by commit sha and loading them into kind"
for sha in $GOOD_SHA $BAD_SHA $FIX_SHA; do docker tag hoofprint/sample-app:dev hoofprint/sample-app:sha-$sha; done
kind load docker-image hoofprint/sample-app:sha-$GOOD_SHA hoofprint/sample-app:sha-$BAD_SHA hoofprint/sample-app:sha-$FIX_SHA --name hoofprint >/dev/null 2>&1

say "resetting sample-app to a plain Deployment at sha-$GOOD_SHA"
kubectl -n sample delete rollout sample-app --ignore-not-found --wait=false >/dev/null 2>&1 || true
kubectl apply -f examples/sample-app/k8s/deployment.yaml >/dev/null
kubectl -n sample patch deployment sample-app --type strategic -p "{\"spec\":{\"template\":{\"spec\":{\"containers\":[{\"name\":\"app\",\"image\":\"hoofprint/sample-app:sha-$GOOD_SHA\",\"env\":[{\"name\":\"INJECT_LATENCY_MS\",\"value\":\"0\"},{\"name\":\"INJECT_ERROR_RATE\",\"value\":\"0\"},{\"name\":\"APP_VERSION\",\"value\":\"v1\"}]}]}}}}" >/dev/null
kubectl -n sample rollout status deployment/sample-app --timeout=120s >/dev/null
kubectl -n sample rollout status deployment/sample-load --timeout=120s >/dev/null

# ship <sha> <latency_ms> <version>: one pod-template change, like a CI deploy.
ship() {
  kubectl -n sample patch deployment sample-app --type strategic -p "{\"spec\":{\"template\":{\"spec\":{\"containers\":[{\"name\":\"app\",\"image\":\"hoofprint/sample-app:sha-$1\",\"env\":[{\"name\":\"INJECT_LATENCY_MS\",\"value\":\"$2\"},{\"name\":\"APP_VERSION\",\"value\":\"$3\"}]}]}}}}" >/dev/null
}

say "starting prometheus port-forward, control plane and agent"
pkill -f 'port-forward svc/prometheus-kube-prometheus-prometheus 9091' 2>/dev/null || true
kubectl -n monitoring port-forward svc/prometheus-kube-prometheus-prometheus 9091:9090 > "$LOGS/pf.log" 2>&1 &
PF=$!
./bin/hoofprint-api --db memory --agent-token dev-token --http :18080 --grpc :19090 \
  --verify-config examples/verify-sample-app.yaml --verify-window "$WINDOW" --verify-settle "$SETTLE" --verify-step "$STEP" > "$LOGS/api.log" 2>&1 &
API=$!
sleep 1
./bin/hoofprint-agent --control-plane localhost:19090 --insecure --token dev-token --cluster kind-hoofprint \
  --tags env=dev --prometheus-url http://localhost:9091 > "$LOGS/agent.log" 2>&1 &
AGENT=$!
trap 'kill $API $AGENT $PF 2>/dev/null || true' EXIT
for i in $(seq 1 30); do grep -q 'workload watcher synced' "$LOGS/agent.log" && break; sleep 1; done
# Let the fresh pods serve traffic long enough to have a baseline window.
say "letting the baseline window fill ($WINDOW)"
sleep "${WINDOW%s}"

capture 01-agents ./bin/hoofprint agents
capture 02-workloads ./bin/hoofprint workloads agent-kind-hoofprint sample

say "shipping a BAD deploy: commit $BAD_SHA adds 300ms of latency, as an agent-written PR would"
ship $BAD_SHA 300 v2
sleep 4
capture 03-deploys ./bin/hoofprint deploys
BAD=$(./bin/hoofprint --json deploys | python3 -c 'import json,sys; print(json.load(sys.stdin)["deploys"][0]["id"])')

wait_verdict() { # wait_verdict <deploy-id>
  for i in $(seq 1 60); do
    n=$(./bin/hoofprint --json deploy "$1" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)["verdicts"]))')
    [ "$n" -ge 1 ] && return 0
    sleep 3
  done
  echo "no verdict for $1 after 3 minutes"; return 1
}
say "waiting for the verdict (settle $SETTLE + window $WINDOW)"
wait_verdict "$BAD"
capture 04-deploy-bad ./bin/hoofprint deploy "$BAD"
BADV=$(./bin/hoofprint --json verdicts --deploy "$BAD" | python3 -c 'import json,sys; print(json.load(sys.stdin)["verdicts"][0]["id"])')
capture 05-explain-bad ./bin/hoofprint explain "$BADV"

say "shipping a GOOD deploy: commit $FIX_SHA reverts it"
ship $FIX_SHA 0 v3
sleep 4
GOOD=$(./bin/hoofprint --json deploys | python3 -c 'import json,sys; print(json.load(sys.stdin)["deploys"][0]["id"])')
say "waiting for the verdict"
wait_verdict "$GOOD"
capture 06-verdicts ./bin/hoofprint verdicts
capture 07-deploy-good ./bin/hoofprint deploy "$GOOD"

say "control plane log"
grep -E 'msg="(verification scheduled|verdict|deploy observed)"' "$LOGS/api.log" | sed -E 's/time=[^ ]+ //; s/level=INFO //' | tee "$OUT/08-api-log.txt"

say "done: captures in $OUT/, logs in $LOGS/. Render screenshots with hack/screenshots.sh"
