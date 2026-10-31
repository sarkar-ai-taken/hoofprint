SHELL := /bin/bash
export GOTOOLCHAIN ?= auto
export PATH := $(HOME)/go/bin:$(PATH)

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/sarkar-ai-taken/hoofprint/internal/version.Version=$(VERSION)
IMAGE_REPO ?= ghcr.io/sarkar-ai-taken

.PHONY: all build test test-race lint vet proto tidy clean images run-api run-agent kind-up kind-down helm-lint ci

all: build

build: ## Build all binaries into bin/
	@mkdir -p bin
	go build -ldflags '$(LDFLAGS)' -o bin/hoofprint-api ./cmd/api
	go build -ldflags '$(LDFLAGS)' -o bin/hoofprint-agent ./cmd/agent
	go build -ldflags '$(LDFLAGS)' -o bin/hoofprint ./cmd/cli
	go build -ldflags '$(LDFLAGS)' -o bin/sample-app ./examples/sample-app

test: ## Unit tests
	go test ./...

test-race: ## Unit tests with the race detector
	go test -race -count=1 ./...

vet:
	go vet ./...

lint: vet ## Static checks (staticcheck if installed)
	@command -v staticcheck >/dev/null && staticcheck ./... || echo "staticcheck not installed; skipped"

proto: ## Regenerate gRPC code (needs buf, protoc-gen-go, protoc-gen-go-grpc in PATH)
	buf lint
	buf generate

tidy:
	go mod tidy

helm-lint: ## Lint and render the chart (default values fail on purpose: token and cluster are required)
	helm lint charts/hoofprint -f charts/hoofprint/values-kind.yaml
	helm template hoofprint charts/hoofprint -f charts/hoofprint/values-kind.yaml >/dev/null
	helm template hoofprint charts/hoofprint --set agentToken=x --set agent.cluster=c --set api.enabled=false --set postgres.enabled=false --set agent.controlPlane=cp.example.com:443 --set agent.insecure=false >/dev/null

ci: vet test helm-lint ## What CI runs

images: ## Build container images
	docker build -f build/api.Dockerfile -t $(IMAGE_REPO)/hoofprint-api:$(VERSION) --build-arg VERSION=$(VERSION) .
	docker build -f build/agent.Dockerfile -t $(IMAGE_REPO)/hoofprint-agent:$(VERSION) --build-arg VERSION=$(VERSION) .
	docker build -f examples/sample-app/Dockerfile -t hoofprint/sample-app:$(VERSION) --build-arg APP_VERSION=$(VERSION) .

run-api: ## Run the control plane locally: in-memory store, verification gate on the sample app metrics (short windows for demos)
	go run ./cmd/api --db memory --agent-token dev-token --log-level debug \
		--verify-config examples/verify-sample-app.yaml --verify-window $${VERIFY_WINDOW:-60s} --verify-settle $${VERIFY_SETTLE:-20s} --verify-step $${VERIFY_STEP:-5s}

run-agent: ## Run the agent locally against the current kubeconfig context (needs: make prom-forward)
	go run ./cmd/agent --control-plane localhost:9090 --insecure --token dev-token --cluster $${CLUSTER:-kind-hoofprint} --tags env=dev --log-level debug \
		--prometheus-url $${PROMETHEUS_URL:-http://localhost:9091}

prom-forward: ## Port-forward the kind cluster's Prometheus to localhost:9091 for a locally run agent
	kubectl -n monitoring port-forward svc/prometheus-kube-prometheus-prometheus 9091:9090

demo: ## Run the scripted demo against the kind cluster and capture CLI output (see hack/demo.sh)
	hack/demo.sh

kind-up: ## Create the dev cluster with Argo Rollouts, Argo CD, Prometheus and the sample app
	hack/kind-up.sh

kind-down:
	hack/kind-down.sh

clean:
	rm -rf bin

help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'
