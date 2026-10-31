// Package verify is the verification gate: when a deploy is observed it
// collects evidence through the agent (metric windows before and after the
// change), runs the judge, and persists the verdict with its evidence bundle.
// Tier 1 compares the same workload pre- vs post-change; tier 2 (canary vs
// baseline side by side) and the LLM judge build on the same shape.
package verify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"text/template"
	"time"

	"github.com/google/uuid"
	"sigs.k8s.io/yaml"

	"github.com/sarkar-ai-taken/hoofprint/internal/agent"
	"github.com/sarkar-ai-taken/hoofprint/internal/controlplane"
	"github.com/sarkar-ai-taken/hoofprint/internal/model"
	"github.com/sarkar-ai-taken/hoofprint/internal/store"
	"github.com/sarkar-ai-taken/hoofprint/internal/verify/stats"
)

// MetricSpec declares one metric to compare. Query is a Go template over
// {{.Namespace}}, {{.Name}}, {{.Kind}}, {{.Cluster}} and the deploy's labels
// via {{.Labels.app}}.
type MetricSpec struct {
	Name      string          `json:"name"`
	Query     string          `json:"query"`
	Direction stats.Direction `json:"direction"` // lower (default) | higher | either
	Critical  *bool           `json:"critical"`  // default true
}

func (m MetricSpec) critical() bool { return m.Critical == nil || *m.Critical }

// Config is the gate's configuration, loaded from YAML.
type Config struct {
	// Window is the length of each comparison window. Default 5m.
	Window time.Duration `json:"-"`
	// Settle is how long after the change to wait before the post window
	// starts, so rolling pods and cold caches are not judged. Default 1m.
	Settle time.Duration `json:"-"`
	// Step is the sample spacing inside a window. Default 15s.
	Step time.Duration `json:"-"`
	// Namespaces limits verification; empty means every namespace the agent
	// watches. System namespaces are always skipped.
	Namespaces []string     `json:"namespaces"`
	Metrics    []MetricSpec `json:"metrics"`
	Judge      stats.Config `json:"judge"`

	// String forms for YAML; parsed into the durations above.
	WindowStr string `json:"window"`
	SettleStr string `json:"settle"`
	StepStr   string `json:"step"`
}

// LoadConfig reads a YAML config file.
func LoadConfig(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	return ParseConfig(b)
}

// ParseConfig parses YAML config bytes and applies defaults.
func ParseConfig(b []byte) (Config, error) {
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("verify config: %w", err)
	}
	var err error
	if c.Window, err = parseDur(c.WindowStr, 5*time.Minute); err != nil {
		return Config{}, fmt.Errorf("verify config: window: %w", err)
	}
	if c.Settle, err = parseDur(c.SettleStr, time.Minute); err != nil {
		return Config{}, fmt.Errorf("verify config: settle: %w", err)
	}
	if c.Step, err = parseDur(c.StepStr, 15*time.Second); err != nil {
		return Config{}, fmt.Errorf("verify config: step: %w", err)
	}
	if len(c.Metrics) == 0 {
		return Config{}, fmt.Errorf("verify config: at least one metric is required")
	}
	for i, m := range c.Metrics {
		if m.Name == "" || m.Query == "" {
			return Config{}, fmt.Errorf("verify config: metric %d needs name and query", i)
		}
		if _, err := template.New(m.Name).Parse(m.Query); err != nil {
			return Config{}, fmt.Errorf("verify config: metric %s: %w", m.Name, err)
		}
	}
	return c, nil
}

func parseDur(s string, def time.Duration) (time.Duration, error) {
	if s == "" {
		return def, nil
	}
	return time.ParseDuration(s)
}

var systemNamespaces = map[string]bool{
	"kube-system": true, "kube-public": true, "kube-node-lease": true, "local-path-storage": true,
	"argo-rollouts": true, "argocd": true, "flagger-system": true, "monitoring": true, "hoofprint": true,
}

// Verifier runs the gate for observed deploys.
type Verifier struct {
	st   store.Store
	exec controlplane.Executor
	cfg  Config
	log  *slog.Logger
	// sleep is swapped in tests.
	sleep func(context.Context, time.Duration) error
}

func New(st store.Store, exec controlplane.Executor, cfg Config, log *slog.Logger) *Verifier {
	if log == nil {
		log = slog.Default()
	}
	return &Verifier{st: st, exec: exec, cfg: cfg, log: log, sleep: sleepCtx}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Wants reports whether the gate applies to this deploy.
func (v *Verifier) Wants(d *model.Deploy) bool {
	if systemNamespaces[d.Namespace] {
		return false
	}
	if len(v.cfg.Namespaces) == 0 {
		return true
	}
	for _, ns := range v.cfg.Namespaces {
		if ns == d.Namespace {
			return true
		}
	}
	return false
}

// HandleDeploy is the controlplane.DeployHandler: it waits for the post
// window to complete, then judges. Safe to call as a goroutine per deploy.
func (v *Verifier) HandleDeploy(ctx context.Context, d *model.Deploy) {
	if !v.Wants(d) {
		return
	}
	postEnd := d.ObservedAt.Add(v.cfg.Settle + v.cfg.Window)
	wait := time.Until(postEnd) + 5*time.Second // let the last scrape land
	v.log.Info("verification scheduled", "deploy", d.ID, "workload", d.Namespace+"/"+d.Name, "verdict_in", wait.Round(time.Second))
	if err := v.sleep(ctx, wait); err != nil {
		return
	}
	verdict, err := v.Run(ctx, d)
	if err != nil {
		v.log.Error("verification failed", "deploy", d.ID, "err", err)
		return
	}
	v.log.Info("verdict", "deploy", d.ID, "workload", d.Namespace+"/"+d.Name, "outcome", verdict.Outcome, "confidence", fmt.Sprintf("%.2f", verdict.Confidence), "rationale", verdict.Rationale)
}

// Run collects evidence for d now and persists a verdict. Exported so it can
// be triggered on demand (CLI, tests).
func (v *Verifier) Run(ctx context.Context, d *model.Deploy) (*model.Verdict, error) {
	ag, err := v.st.GetAgent(ctx, d.AgentID)
	if err != nil {
		return nil, fmt.Errorf("agent %s: %w", d.AgentID, err)
	}
	if !ag.HasCapability("prometheus") {
		return v.persist(ctx, d, stats.Result{
			Outcome: model.OutcomeUnsure, Confidence: 0,
			Rationale: fmt.Sprintf("agent %s has no prometheus capability; start it with --prometheus-url to enable metric evidence", ag.ID),
		}, nil)
	}

	base := window{start: d.ObservedAt.Add(-v.cfg.Window), end: d.ObservedAt}
	post := window{start: d.ObservedAt.Add(v.cfg.Settle), end: d.ObservedAt.Add(v.cfg.Settle + v.cfg.Window)}

	var metrics []stats.Metric
	var raws []rawEvidence
	for _, spec := range v.cfg.Metrics {
		q, err := renderQuery(spec.Query, d)
		if err != nil {
			return nil, err
		}
		before, bRaw, err := v.query(ctx, d.AgentID, q, base)
		if err != nil {
			return nil, fmt.Errorf("metric %s (baseline): %w", spec.Name, err)
		}
		after, aRaw, err := v.query(ctx, d.AgentID, q, post)
		if err != nil {
			return nil, fmt.Errorf("metric %s (post): %w", spec.Name, err)
		}
		dir := spec.Direction
		if dir == "" {
			dir = stats.LowerIsBetter
		}
		metrics = append(metrics, stats.Metric{Name: spec.Name, Query: q, Baseline: before, Canary: after, Direction: dir, Critical: spec.critical()})
		raws = append(raws, rawEvidence{name: spec.Name, query: q, baseline: bRaw, post: aRaw, baseWin: base, postWin: post})
	}
	res := stats.Judge(v.cfg.Judge, metrics)
	return v.persist(ctx, d, res, raws)
}

type window struct{ start, end time.Time }

type rawEvidence struct {
	name, query      string
	baseline, post   agent.PromQueryRangeOutput
	baseWin, postWin window
}

// query runs one range query through the agent and flattens the result to
// samples. Multiple series (one per pod) are pooled: tier 1 compares the
// workload as a whole.
func (v *Verifier) query(ctx context.Context, agentID, q string, w window) ([]float64, agent.PromQueryRangeOutput, error) {
	payload, _ := json.Marshal(agent.PromQueryRangeInput{Query: q, Start: w.start, End: w.end, Step: v.cfg.Step.String()})
	res, err := v.exec.Execute(ctx, controlplane.WorkRequest{AgentID: agentID, Kind: agent.KindPrometheusQueryRange, Payload: payload})
	if err != nil {
		return nil, agent.PromQueryRangeOutput{}, err
	}
	if !res.OK {
		return nil, agent.PromQueryRangeOutput{}, fmt.Errorf("agent: %s", res.Error)
	}
	var out agent.PromQueryRangeOutput
	if err := json.Unmarshal(res.Payload, &out); err != nil {
		return nil, agent.PromQueryRangeOutput{}, fmt.Errorf("decode: %w", err)
	}
	var samples []float64
	for _, s := range out.Series {
		for _, p := range s.Values {
			samples = append(samples, p.V)
		}
	}
	return samples, out, nil
}

func renderQuery(tpl string, d *model.Deploy) (string, error) {
	t, err := template.New("q").Option("missingkey=zero").Parse(tpl)
	if err != nil {
		return "", err
	}
	labels := d.Labels
	if labels == nil {
		labels = map[string]string{}
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, map[string]any{
		"Namespace": d.Namespace, "Name": d.Name, "Kind": d.Kind, "Cluster": d.Cluster, "Labels": labels,
	}); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}

// metricEvidence is the JSON payload of one "metric" evidence record.
type metricEvidence struct {
	stats.MetricVerdict
	BaselineWindow [2]time.Time               `json:"baseline_window"`
	PostWindow     [2]time.Time               `json:"post_window"`
	Baseline       agent.PromQueryRangeOutput `json:"baseline"`
	Post           agent.PromQueryRangeOutput `json:"post"`
}

func (v *Verifier) persist(ctx context.Context, d *model.Deploy, res stats.Result, raws []rawEvidence) (*model.Verdict, error) {
	verdict := &model.Verdict{
		ID: uuid.NewString(), DeployID: d.ID, Judge: "stats",
		Outcome: res.Outcome, Confidence: res.Confidence, Rationale: res.Rationale,
	}
	var evidence []*model.Evidence
	for i, mv := range res.Metrics {
		ev := metricEvidence{MetricVerdict: mv}
		if i < len(raws) {
			r := raws[i]
			ev.BaselineWindow = [2]time.Time{r.baseWin.start, r.baseWin.end}
			ev.PostWindow = [2]time.Time{r.postWin.start, r.postWin.end}
			ev.Baseline, ev.Post = r.baseline, r.post
		}
		payload, _ := json.Marshal(ev)
		evidence = append(evidence, &model.Evidence{
			ID: uuid.NewString(), Kind: "metric",
			Title: fmt.Sprintf("%s: %s", mv.Name, mv.Reason), Payload: payload,
		})
	}
	// The change itself is evidence too: what moved in the pod template.
	changePayload, _ := json.Marshal(map[string]any{
		"images_before": d.ImagesBefore, "images_after": d.ImagesAfter,
		"revision_before": d.RevisionBefore, "revision_after": d.RevisionAfter,
		"template_hash_before": d.TemplateHashBefore, "template_hash_after": d.TemplateHashAfter,
	})
	evidence = append(evidence, &model.Evidence{
		ID: uuid.NewString(), Kind: "change", Title: changeTitle(d), Payload: changePayload,
	})
	if err := v.st.InsertVerdict(ctx, verdict, evidence); err != nil {
		return nil, fmt.Errorf("persist verdict: %w", err)
	}
	return verdict, nil
}

func changeTitle(d *model.Deploy) string {
	for i, a := range d.ImagesAfter {
		if i < len(d.ImagesBefore) && d.ImagesBefore[i].Image != a.Image {
			return fmt.Sprintf("image %s: %s -> %s", a.Container, d.ImagesBefore[i].Image, a.Image)
		}
	}
	if d.RevisionBefore != d.RevisionAfter && d.RevisionAfter != "" {
		return fmt.Sprintf("revision %s -> %s", d.RevisionBefore, d.RevisionAfter)
	}
	return fmt.Sprintf("%s %s/%s pod template changed (%s -> %s), same image", d.Kind, d.Namespace, d.Name, d.TemplateHashBefore, d.TemplateHashAfter)
}
