package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sarkar-ai-taken/hoofprint/internal/controlplane"
	"github.com/sarkar-ai-taken/hoofprint/internal/model"
	"github.com/sarkar-ai-taken/hoofprint/internal/store/memory"
)

type fakeExec struct {
	last controlplane.WorkRequest
	err  error
}

func (f *fakeExec) Execute(_ context.Context, req controlplane.WorkRequest) (*controlplane.WorkResult, error) {
	f.last = req
	if f.err != nil {
		return nil, f.err
	}
	return &controlplane.WorkResult{WorkID: "w1", AgentID: req.AgentID, OK: true, Payload: json.RawMessage(`{"n":1}`)}, nil
}

func setup(t *testing.T) (*httptest.Server, *memory.Store, *fakeExec) {
	st := memory.New()
	ex := &fakeExec{}
	srv := httptest.NewServer(New(st, ex, nil, "test").Handler())
	t.Cleanup(srv.Close)
	ctx := context.Background()
	_ = st.UpsertAgent(ctx, &model.Agent{ID: "a1", Cluster: "kind", Status: model.AgentOnline, Capabilities: []string{"kubernetes"}})
	_ = st.InsertDeploy(ctx, &model.Deploy{ID: "d1", AgentID: "a1", Cluster: "kind", Namespace: "default", Kind: "Deployment", Name: "api", Tier: model.TierWorkload, ObservedAt: time.Now()})
	_ = st.InsertDeploy(ctx, &model.Deploy{ID: "d2", AgentID: "a1", Cluster: "kind", Namespace: "jobs", Kind: "Deployment", Name: "worker", Tier: model.TierProgressive, ObservedAt: time.Now().Add(time.Second)})
	_ = st.InsertVerdict(ctx, &model.Verdict{ID: "v1", DeployID: "d1", Judge: "stats", Outcome: model.OutcomeFail, Confidence: 0.9, Rationale: "latency up"},
		[]*model.Evidence{{ID: "e1", Kind: "metric", Title: "p99", Payload: []byte(`{"x":1}`)}})
	return srv, st, ex
}

func get(t *testing.T, url string, out any) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode
}

func TestReadEndpoints(t *testing.T) {
	srv, _, _ := setup(t)

	var agents struct{ Agents []model.Agent }
	if code := get(t, srv.URL+"/api/v1/agents", &agents); code != 200 || len(agents.Agents) != 1 || agents.Agents[0].ID != "a1" {
		t.Fatalf("agents: %d %+v", code, agents)
	}
	var agent model.Agent
	if code := get(t, srv.URL+"/api/v1/agents/a1", &agent); code != 200 || agent.Cluster != "kind" {
		t.Fatalf("agent: %d %+v", code, agent)
	}
	if code := get(t, srv.URL+"/api/v1/agents/zz", nil); code != 404 {
		t.Fatalf("missing agent: %d", code)
	}

	var deploys struct{ Deploys []model.Deploy }
	if code := get(t, srv.URL+"/api/v1/deploys", &deploys); code != 200 || len(deploys.Deploys) != 2 || deploys.Deploys[0].ID != "d2" {
		t.Fatalf("deploys: %d %+v", code, deploys)
	}
	if get(t, srv.URL+"/api/v1/deploys?namespace=jobs", &deploys); len(deploys.Deploys) != 1 {
		t.Fatalf("namespace filter: %+v", deploys)
	}
	var detail struct {
		ID       string          `json:"id"`
		Tier     string          `json:"tier"`
		Verdicts []model.Verdict `json:"verdicts"`
	}
	if code := get(t, srv.URL+"/api/v1/deploys/d1", &detail); code != 200 || detail.Tier != "workload" || len(detail.Verdicts) != 1 {
		t.Fatalf("deploy detail: %d %+v", code, detail)
	}
	var vd struct {
		Outcome  string           `json:"outcome"`
		Evidence []model.Evidence `json:"evidence"`
	}
	if code := get(t, srv.URL+"/api/v1/verdicts/v1", &vd); code != 200 || vd.Outcome != "fail" || len(vd.Evidence) != 1 || string(vd.Evidence[0].Payload) != `{"x":1}` {
		t.Fatalf("verdict detail: %d %+v", code, vd)
	}
	var vs struct{ Verdicts []model.Verdict }
	if get(t, srv.URL+"/api/v1/verdicts?deploy_id=d2", &vs); len(vs.Verdicts) != 0 {
		t.Fatalf("verdict filter: %+v", vs)
	}
	if code := get(t, srv.URL+"/healthz", nil); code != 200 {
		t.Fatalf("healthz: %d", code)
	}
}

func TestRunWork(t *testing.T) {
	srv, _, ex := setup(t)
	resp, err := http.Post(srv.URL+"/api/v1/agents/a1/work", "application/json", strings.NewReader(`{"kind":"workloads.list","payload":{"namespace":"default"}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var res controlplane.WorkResult
	_ = json.NewDecoder(resp.Body).Decode(&res)
	if resp.StatusCode != 200 || !res.OK || ex.last.AgentID != "a1" || ex.last.Kind != "workloads.list" || string(ex.last.Payload) != `{"namespace":"default"}` {
		t.Fatalf("work: %d %+v %+v", resp.StatusCode, res, ex.last)
	}

	resp, _ = http.Post(srv.URL+"/api/v1/agents/a1/work", "application/json", strings.NewReader(`{}`))
	if resp.StatusCode != 400 {
		t.Fatalf("missing kind: %d", resp.StatusCode)
	}
	resp.Body.Close()

	ex.err = controlplane.ErrNoAgent
	resp, _ = http.Post(srv.URL+"/api/v1/agents/gone/work", "application/json", strings.NewReader(`{"kind":"ping"}`))
	if resp.StatusCode != 404 {
		t.Fatalf("no agent: %d", resp.StatusCode)
	}
	resp.Body.Close()

	ex.err = context.DeadlineExceeded
	resp, _ = http.Post(srv.URL+"/api/v1/agents/a1/work", "application/json", strings.NewReader(`{"kind":"ping"}`))
	if resp.StatusCode != 504 {
		t.Fatalf("timeout: %d", resp.StatusCode)
	}
	resp.Body.Close()
}
