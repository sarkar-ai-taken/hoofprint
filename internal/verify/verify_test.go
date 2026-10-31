package verify

import (
	"context"
	"encoding/json"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/sarkar-ai-taken/hoofprint/internal/agent"
	"github.com/sarkar-ai-taken/hoofprint/internal/controlplane"
	"github.com/sarkar-ai-taken/hoofprint/internal/model"
	"github.com/sarkar-ai-taken/hoofprint/internal/store/memory"
)

const sampleConfig = `
window: 60s
settle: 20s
step: 5s
metrics:
  - name: p99_latency_seconds
    query: histogram_quantile(0.99, sum by (le) (rate(http_request_duration_seconds_bucket{namespace="{{.Namespace}}",service="{{.Name}}"}[30s])))
  - name: error_rate
    query: sum(rate(http_requests_total{namespace="{{.Namespace}}",code=~"5.."}[30s])) / sum(rate(http_requests_total{namespace="{{.Namespace}}"}[30s]))
  - name: rps
    query: sum(rate(http_requests_total{namespace="{{.Namespace}}"}[30s]))
    direction: higher
    critical: false
`

func TestParseConfig(t *testing.T) {
	c, err := ParseConfig([]byte(sampleConfig))
	if err != nil {
		t.Fatal(err)
	}
	if c.Window != time.Minute || c.Settle != 20*time.Second || c.Step != 5*time.Second || len(c.Metrics) != 3 {
		t.Fatalf("%+v", c)
	}
	if !c.Metrics[0].critical() || c.Metrics[2].critical() {
		t.Fatal("critical defaults")
	}
	if _, err := ParseConfig([]byte("metrics: []")); err == nil {
		t.Fatal("empty metrics should fail")
	}
	if _, err := ParseConfig([]byte("metrics:\n  - name: x\n    query: '{{.Bad'")); err == nil {
		t.Fatal("bad template should fail")
	}
	if _, err := ParseConfig([]byte("window: nope\nmetrics:\n  - name: x\n    query: up")); err == nil {
		t.Fatal("bad duration should fail")
	}
}

func TestRenderQuery(t *testing.T) {
	d := &model.Deploy{Namespace: "sample", Name: "sample-app", Kind: "Deployment", Labels: map[string]string{"app": "shop"}}
	q, err := renderQuery(`up{ns="{{.Namespace}}",name="{{.Name}}",app="{{.Labels.app}}",missing="{{.Labels.nope}}"}`, d)
	if err != nil {
		t.Fatal(err)
	}
	if q != `up{ns="sample",name="sample-app",app="shop",missing=""}` {
		t.Fatalf("rendered: %s", q)
	}
}

// fakeExec answers prometheus.query_range with canned series depending on
// whether the window is before or after the deploy.
type fakeExec struct {
	observed time.Time
	latency  func(post bool) []float64
	errors   func(post bool) []float64
	calls    int
}

func (f *fakeExec) Execute(_ context.Context, req controlplane.WorkRequest) (*controlplane.WorkResult, error) {
	f.calls++
	var in agent.PromQueryRangeInput
	_ = json.Unmarshal(req.Payload, &in)
	post := in.Start.After(f.observed) || in.Start.Equal(f.observed)
	var vals []float64
	switch {
	case strings.Contains(in.Query, "duration"):
		vals = f.latency(post)
	case strings.Contains(in.Query, `5..`):
		vals = f.errors(post)
	default:
		vals = constant(12, 100)
	}
	series := agent.PromSeries{Metric: map[string]string{}}
	for i, v := range vals {
		series.Values = append(series.Values, agent.PromSample{T: in.Start.Add(time.Duration(i) * 5 * time.Second), V: v})
	}
	payload, _ := json.Marshal(agent.PromQueryRangeOutput{Series: []agent.PromSeries{series}})
	return &controlplane.WorkResult{OK: true, Payload: payload, AgentID: req.AgentID}, nil
}

func constant(n int, v float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func noisy(rng *rand.Rand, n int, mean, sd float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = mean + sd*rng.NormFloat64()
	}
	return out
}

func setup(t *testing.T, caps []string) (*memory.Store, *model.Deploy) {
	st := memory.New()
	ctx := context.Background()
	if err := st.UpsertAgent(ctx, &model.Agent{ID: "a1", Cluster: "kind", Capabilities: caps, Status: model.AgentOnline}); err != nil {
		t.Fatal(err)
	}
	d := &model.Deploy{
		ID: "d1", AgentID: "a1", Cluster: "kind", Namespace: "sample", Kind: "Deployment", Name: "sample-app", Tier: model.TierWorkload,
		ImagesBefore:   []model.ContainerImage{{Container: "app", Image: "app:sha-aaaaaaa"}},
		ImagesAfter:    []model.ContainerImage{{Container: "app", Image: "app:sha-bbbbbbb"}},
		RevisionBefore: "aaaaaaa", RevisionAfter: "bbbbbbb", TemplateHashBefore: "h1", TemplateHashAfter: "h2",
		ObservedAt: time.Now().Add(-2 * time.Minute),
	}
	if err := st.InsertDeploy(ctx, d); err != nil {
		t.Fatal(err)
	}
	return st, d
}

func TestRunFailsOnLatencyRegression(t *testing.T) {
	cfg, _ := ParseConfig([]byte(sampleConfig))
	st, d := setup(t, []string{"kubernetes", "prometheus"})
	rng := rand.New(rand.NewSource(1))
	ex := &fakeExec{observed: d.ObservedAt,
		latency: func(post bool) []float64 {
			if post {
				return noisy(rng, 12, 0.33, 0.02)
			}
			return noisy(rng, 12, 0.03, 0.005)
		},
		errors: func(bool) []float64 { return constant(12, 0) },
	}
	v := New(st, ex, cfg, nil)
	verdict, err := v.Run(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Outcome != model.OutcomeFail || verdict.Confidence < 0.9 {
		t.Fatalf("verdict: %+v", verdict)
	}
	if !strings.Contains(verdict.Rationale, "p99_latency_seconds") {
		t.Fatalf("rationale should name the metric: %s", verdict.Rationale)
	}
	if ex.calls != 6 { // 3 metrics x 2 windows
		t.Fatalf("calls: %d", ex.calls)
	}
	got, evs, err := st.GetVerdict(context.Background(), verdict.ID)
	if err != nil || got.DeployID != "d1" {
		t.Fatal(err)
	}
	if len(evs) != 4 { // 3 metric + 1 change
		t.Fatalf("evidence: %d", len(evs))
	}
	var me metricEvidence
	if err := json.Unmarshal(evs[0].Payload, &me); err != nil || me.Name != "p99_latency_seconds" || me.Outcome != model.OutcomeFail || len(me.Baseline.Series) != 1 {
		t.Fatalf("metric evidence: %+v %v", me, err)
	}
	if !me.BaselineWindow[1].Equal(d.ObservedAt) || !me.PostWindow[0].Equal(d.ObservedAt.Add(20*time.Second)) {
		t.Fatalf("windows: %v %v", me.BaselineWindow, me.PostWindow)
	}
	if evs[3].Kind != "change" || !strings.Contains(evs[3].Title, "sha-bbbbbbb") {
		t.Fatalf("change evidence: %+v", evs[3])
	}
}

func TestRunPassesWhenUnchanged(t *testing.T) {
	cfg, _ := ParseConfig([]byte(sampleConfig))
	st, d := setup(t, []string{"kubernetes", "prometheus"})
	rng := rand.New(rand.NewSource(2))
	ex := &fakeExec{observed: d.ObservedAt,
		latency: func(bool) []float64 { return noisy(rng, 12, 0.03, 0.005) },
		errors:  func(bool) []float64 { return noisy(rng, 12, 0.01, 0.002) },
	}
	verdict, err := New(st, ex, cfg, nil).Run(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Outcome != model.OutcomePass {
		t.Fatalf("verdict: %+v", verdict)
	}
}

func TestRunWithoutPrometheusIsUnsure(t *testing.T) {
	cfg, _ := ParseConfig([]byte(sampleConfig))
	st, d := setup(t, []string{"kubernetes"})
	ex := &fakeExec{observed: d.ObservedAt}
	verdict, err := New(st, ex, cfg, nil).Run(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Outcome != model.OutcomeUnsure || ex.calls != 0 || !strings.Contains(verdict.Rationale, "prometheus") {
		t.Fatalf("verdict: %+v calls=%d", verdict, ex.calls)
	}
}

func TestHandleDeployWaitsForPostWindow(t *testing.T) {
	cfg, _ := ParseConfig([]byte(sampleConfig))
	st, d := setup(t, []string{"kubernetes", "prometheus"})
	d.ObservedAt = time.Now()
	ex := &fakeExec{observed: d.ObservedAt,
		latency: func(bool) []float64 { return constant(12, 0.03) },
		errors:  func(bool) []float64 { return constant(12, 0) },
	}
	v := New(st, ex, cfg, nil)
	var slept time.Duration
	v.sleep = func(_ context.Context, dur time.Duration) error { slept = dur; return nil }
	v.HandleDeploy(context.Background(), d)
	// settle 20s + window 60s + 5s grace, minus the few ms since ObservedAt.
	if slept < 84*time.Second || slept > 86*time.Second {
		t.Fatalf("slept %v", slept)
	}
	vs, _ := st.ListVerdicts(context.Background(), "d1")
	if len(vs) != 1 {
		t.Fatalf("expected a verdict, got %d", len(vs))
	}
	// System namespaces are skipped entirely.
	sys := *d
	sys.ID = "d2"
	sys.Namespace = "kube-system"
	_ = st.InsertDeploy(context.Background(), &sys)
	if v.Wants(&sys) {
		t.Fatal("kube-system should be skipped")
	}
}

func TestExampleConfigLoads(t *testing.T) {
	c, err := LoadConfig("../../examples/verify-sample-app.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if c.Judge.Alpha != 0.05 || c.Judge.MinEffect != 0.33 || c.Judge.MinSamples != 5 {
		t.Fatalf("judge config not parsed: %+v", c.Judge)
	}
	if len(c.Metrics) != 3 || c.Namespaces[0] != "sample" || c.Window != 5*time.Minute {
		t.Fatalf("%+v", c)
	}
	d := &model.Deploy{Namespace: "sample", Name: "sample-app"}
	q, err := renderQuery(c.Metrics[1].Query, d)
	if err != nil || !strings.Contains(q, `service="sample-app", code=~"5.."`) || !strings.Contains(q, "or vector(0)") {
		t.Fatalf("rendered error_rate: %s %v", q, err)
	}
}
