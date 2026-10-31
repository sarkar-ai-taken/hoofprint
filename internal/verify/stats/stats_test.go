package stats

import (
	"math"
	"math/rand"
	"strings"
	"testing"

	"github.com/sarkar-ai-taken/hoofprint/internal/model"
)

func TestMannWhitneyKnownValues(t *testing.T) {
	// Classic textbook example (no ties): A = {1,2,3,4,5}, B = {6,7,8,9,10}.
	// U_A = 0 (every A < every B), delta = -1, strongly significant.
	r := MannWhitney([]float64{1, 2, 3, 4, 5}, []float64{6, 7, 8, 9, 10})
	if r.U != 0 || r.CliffsDelta != -1 {
		t.Fatalf("U=%v delta=%v", r.U, r.CliffsDelta)
	}
	if r.P > 0.02 {
		t.Fatalf("expected significant, p=%v", r.P)
	}

	// Identical distributions: U = na*nb/2, delta 0, p = 1.
	r = MannWhitney([]float64{1, 1, 1}, []float64{1, 1, 1})
	if r.CliffsDelta != 0 || r.P != 1 {
		t.Fatalf("all ties: %+v", r)
	}

	// Interleaved samples: no evidence of shift.
	r = MannWhitney([]float64{1, 3, 5, 7, 9}, []float64{2, 4, 6, 8, 10})
	if r.P < 0.5 {
		t.Fatalf("interleaved should be insignificant, p=%v", r.P)
	}

	// Symmetric: swapping samples negates delta and keeps p.
	a := []float64{3, 5, 5, 8, 12}
	b := []float64{2, 5, 6, 7, 7, 9}
	r1, r2 := MannWhitney(a, b), MannWhitney(b, a)
	if math.Abs(r1.P-r2.P) > 1e-12 || math.Abs(r1.CliffsDelta+r2.CliffsDelta) > 1e-12 {
		t.Fatalf("asymmetric: %+v vs %+v", r1, r2)
	}

	// Empty input is handled.
	if r := MannWhitney(nil, b); r.P != 1 {
		t.Fatalf("empty: %+v", r)
	}
}

func TestMannWhitneyHandComputed(t *testing.T) {
	// Hand-computed: a beats b in 35 of 50 pairs -> U=35. mu=25, sigma^2=10*5*16/12,
	// continuity-corrected z = 9.5/8.165 = 1.1635 -> two-sided p ≈ 0.2448.
	a := []float64{0.8, 0.83, 1.89, 1.04, 1.45, 1.38, 1.91, 1.64, 0.73, 1.46}
	b := []float64{1.15, 0.88, 0.9, 0.74, 1.21}
	r := MannWhitney(a, b)
	if r.U != 35 {
		t.Fatalf("U=%v want 35", r.U)
	}
	if math.Abs(r.P-0.2448) > 0.002 {
		t.Fatalf("p=%v want ~0.2448", r.P)
	}
	if math.Abs(r.CliffsDelta-0.4) > 1e-12 {
		t.Fatalf("delta=%v want 0.4", r.CliffsDelta)
	}
}

func TestMedian(t *testing.T) {
	if Median(nil) != 0 || Median([]float64{3}) != 3 || Median([]float64{1, 3}) != 2 || Median([]float64{5, 1, 3}) != 3 {
		t.Fatal("median")
	}
}

func gaussian(rng *rand.Rand, n int, mean, sd float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = mean + sd*rng.NormFloat64()
	}
	return out
}

func TestJudgeDetectsLatencyRegression(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	res := Judge(Config{}, []Metric{
		{Name: "p99_latency_ms", Baseline: gaussian(rng, 30, 120, 10), Canary: gaussian(rng, 30, 480, 40), Direction: LowerIsBetter, Critical: true},
		{Name: "error_rate", Baseline: gaussian(rng, 30, 0.01, 0.002), Canary: gaussian(rng, 30, 0.01, 0.002), Direction: LowerIsBetter, Critical: true},
	})
	if res.Outcome != model.OutcomeFail {
		t.Fatalf("want fail, got %s: %s", res.Outcome, res.Rationale)
	}
	if res.Confidence < 0.9 {
		t.Fatalf("confidence too low: %v", res.Confidence)
	}
	if res.Metrics[0].Outcome != model.OutcomeFail || res.Metrics[1].Outcome != model.OutcomePass {
		t.Fatalf("per-metric: %+v", res.Metrics)
	}
	if res.Metrics[0].PctChange < 250 {
		t.Fatalf("pct change: %v", res.Metrics[0].PctChange)
	}
}

func TestJudgePassesOnNoise(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	res := Judge(Config{}, []Metric{
		{Name: "p99_latency_ms", Baseline: gaussian(rng, 40, 120, 15), Canary: gaussian(rng, 40, 122, 15), Direction: LowerIsBetter, Critical: true},
		{Name: "rps", Baseline: gaussian(rng, 40, 1000, 50), Canary: gaussian(rng, 40, 1010, 50), Direction: HigherIsBetter, Critical: true},
	})
	if res.Outcome != model.OutcomePass {
		t.Fatalf("want pass, got %s: %s", res.Outcome, res.Rationale)
	}
}

func TestJudgeImprovementIsNotRegression(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	res := Judge(Config{}, []Metric{
		{Name: "p99_latency_ms", Baseline: gaussian(rng, 30, 120, 10), Canary: gaussian(rng, 30, 60, 5), Direction: LowerIsBetter, Critical: true},
	})
	if res.Outcome != model.OutcomePass {
		t.Fatalf("faster canary should pass, got %s: %s", res.Outcome, res.Rationale)
	}
	if res.Confidence < 0.95 || !res.Metrics[0].Improved || !strings.Contains(res.Rationale, "Improved: p99_latency_ms") {
		t.Fatalf("an improvement should be a confident pass: conf=%v rationale=%q", res.Confidence, res.Rationale)
	}
	// But with Direction Either, any shift is a regression.
	res = Judge(Config{}, []Metric{
		{Name: "rps", Baseline: gaussian(rng, 30, 120, 10), Canary: gaussian(rng, 30, 60, 5), Direction: Either, Critical: true},
	})
	if res.Outcome != model.OutcomeFail {
		t.Fatalf("either-direction shift should fail, got %s", res.Outcome)
	}
}

func TestJudgeUnsureCases(t *testing.T) {
	// Too few samples.
	res := Judge(Config{}, []Metric{{Name: "x", Baseline: []float64{1, 2}, Canary: []float64{9, 9}, Critical: true}})
	if res.Outcome != model.OutcomeUnsure {
		t.Fatalf("too few samples should be unsure, got %s", res.Outcome)
	}
	// Non-critical regression only.
	rng := rand.New(rand.NewSource(4))
	res = Judge(Config{}, []Metric{
		{Name: "cpu", Baseline: gaussian(rng, 30, 0.5, 0.05), Canary: gaussian(rng, 30, 0.9, 0.05), Direction: LowerIsBetter, Critical: false},
		{Name: "p99", Baseline: gaussian(rng, 30, 100, 5), Canary: gaussian(rng, 30, 100, 5), Direction: LowerIsBetter, Critical: true},
	})
	if res.Outcome != model.OutcomeUnsure {
		t.Fatalf("non-critical regression should be unsure, got %s: %s", res.Outcome, res.Rationale)
	}
	// Nothing declared.
	if Judge(Config{}, nil).Outcome != model.OutcomeUnsure {
		t.Fatal("no metrics should be unsure")
	}
}
