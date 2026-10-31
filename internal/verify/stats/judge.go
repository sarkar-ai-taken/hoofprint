package stats

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/sarkar-ai-taken/hoofprint/internal/model"
)

// Direction says which way a metric is allowed to move.
type Direction string

const (
	// LowerIsBetter: an increase in the canary is a regression (latency, errors).
	LowerIsBetter Direction = "lower"
	// HigherIsBetter: a decrease in the canary is a regression (throughput).
	HigherIsBetter Direction = "higher"
	// Either: any significant shift is a regression.
	Either Direction = "either"
)

// Metric is one declared comparison. Baseline and Canary are raw samples
// (one per scrape), not pre-aggregated.
type Metric struct {
	Name      string
	Query     string // the Prometheus query, for the evidence record
	Baseline  []float64
	Canary    []float64
	Direction Direction
	// Critical metrics fail the whole deploy on their own. Non-critical ones
	// only contribute to "unsure".
	Critical bool
}

// Config tunes the judge. Zero values take the defaults below.
type Config struct {
	// Alpha is the significance threshold for the p-value. Default 0.05.
	Alpha float64
	// MinEffect is the smallest |Cliff's delta| treated as a real change.
	// Default 0.33 (conventional "medium").
	MinEffect float64
	// MinSamples per side below which a metric is "unsure". Default 5.
	MinSamples int
}

func (c Config) withDefaults() Config {
	if c.Alpha == 0 {
		c.Alpha = 0.05
	}
	if c.MinEffect == 0 {
		c.MinEffect = 0.33
	}
	if c.MinSamples == 0 {
		c.MinSamples = 5
	}
	return c
}

// MetricVerdict is the per-metric evidence the judge produces.
type MetricVerdict struct {
	Name           string        `json:"name"`
	Query          string        `json:"query,omitempty"`
	Outcome        model.Outcome `json:"outcome"`
	Direction      Direction     `json:"direction"`
	Critical       bool          `json:"critical"`
	BaselineMedian float64       `json:"baseline_median"`
	CanaryMedian   float64       `json:"canary_median"`
	// PctChange is (canary - baseline)/baseline * 100, NaN-safe (0 when
	// baseline is 0).
	PctChange   float64 `json:"pct_change"`
	PValue      float64 `json:"p_value"`
	CliffsDelta float64 `json:"cliffs_delta"`
	BaselineN   int     `json:"baseline_n"`
	CanaryN     int     `json:"canary_n"`
	// Improved is set on a pass that was a significant move in the good
	// direction (as opposed to no detectable change).
	Improved bool   `json:"improved,omitempty"`
	Reason   string `json:"reason"`
}

// Result is the judge's aggregate call plus the per-metric breakdown.
type Result struct {
	Outcome    model.Outcome   `json:"outcome"`
	Confidence float64         `json:"confidence"`
	Rationale  string          `json:"rationale"`
	Metrics    []MetricVerdict `json:"metrics"`
}

// Judge evaluates every metric and aggregates:
//
//   - any critical metric regressing          -> fail
//   - no regressions, every metric conclusive -> pass
//   - otherwise                               -> unsure
//
// Confidence is 1 - p of the decisive metric (the worst regression for fail,
// the least conclusive metric for pass), scaled by effect size.
func Judge(cfg Config, metrics []Metric) Result {
	cfg = cfg.withDefaults()
	res := Result{Outcome: model.OutcomePass, Confidence: 1}
	if len(metrics) == 0 {
		res.Outcome = model.OutcomeUnsure
		res.Confidence = 0
		res.Rationale = "no metrics declared; nothing to compare"
		return res
	}

	var fails, unsures []MetricVerdict
	var improved, unchanged []MetricVerdict
	minPassConf := 1.0
	for _, m := range metrics {
		mv := judgeMetric(cfg, m)
		res.Metrics = append(res.Metrics, mv)
		switch mv.Outcome {
		case model.OutcomeFail:
			if mv.Critical {
				fails = append(fails, mv)
			} else {
				unsures = append(unsures, mv)
			}
		case model.OutcomeUnsure:
			unsures = append(unsures, mv)
		case model.OutcomePass:
			if mv.Improved {
				// A significant move in the good direction is a confident pass.
				improved = append(improved, mv)
				if c := clamp(1-mv.PValue, 0, 1); c < minPassConf {
					minPassConf = c
				}
			} else {
				unchanged = append(unchanged, mv)
				if c := passConfidence(mv); c < minPassConf {
					minPassConf = c
				}
			}
		}
	}

	switch {
	case len(fails) > 0:
		sort.Slice(fails, func(i, j int) bool {
			return math.Abs(fails[i].CliffsDelta) > math.Abs(fails[j].CliffsDelta)
		})
		worst := fails[0]
		res.Outcome = model.OutcomeFail
		res.Confidence = clamp((1-worst.PValue)*math.Min(1, math.Abs(worst.CliffsDelta)/0.5), 0, 1)
		names := make([]string, 0, len(fails))
		for _, f := range fails {
			names = append(names, f.Name)
		}
		res.Rationale = fmt.Sprintf("%d critical metric(s) regressed (%s). Worst: %s", len(fails), strings.Join(names, ", "), worst.Reason)
	case len(unsures) > 0:
		res.Outcome = model.OutcomeUnsure
		res.Confidence = 0.5
		parts := make([]string, 0, len(unsures))
		for _, u := range unsures {
			parts = append(parts, u.Name+": "+u.Reason)
		}
		res.Rationale = "no critical regression, but inconclusive: " + strings.Join(parts, "; ")
	default:
		res.Confidence = minPassConf
		if len(improved) == 0 {
			res.Rationale = fmt.Sprintf("all %d metric(s) within baseline (max |delta| %.2f)", len(metrics), maxAbsDelta(res.Metrics))
			break
		}
		parts := make([]string, 0, len(improved))
		for _, m := range improved {
			parts = append(parts, fmt.Sprintf("%s %+.0f%%", m.Name, m.PctChange))
		}
		res.Rationale = fmt.Sprintf("no regressions. Improved: %s", strings.Join(parts, ", "))
		if len(unchanged) > 0 {
			names := make([]string, 0, len(unchanged))
			for _, m := range unchanged {
				names = append(names, m.Name)
			}
			res.Rationale += fmt.Sprintf(". Unchanged: %s", strings.Join(names, ", "))
		}
	}
	return res
}

func judgeMetric(cfg Config, m Metric) MetricVerdict {
	mv := MetricVerdict{
		Name: m.Name, Query: m.Query, Direction: m.Direction, Critical: m.Critical,
		BaselineN: len(m.Baseline), CanaryN: len(m.Canary),
		BaselineMedian: Median(m.Baseline), CanaryMedian: Median(m.Canary),
	}
	if mv.Direction == "" {
		mv.Direction = LowerIsBetter
	}
	if mv.BaselineMedian != 0 {
		mv.PctChange = (mv.CanaryMedian - mv.BaselineMedian) / math.Abs(mv.BaselineMedian) * 100
	}
	if len(m.Baseline) < cfg.MinSamples || len(m.Canary) < cfg.MinSamples {
		mv.Outcome = model.OutcomeUnsure
		mv.PValue = 1
		mv.Reason = fmt.Sprintf("too few samples (baseline %d, canary %d, need %d each)", len(m.Baseline), len(m.Canary), cfg.MinSamples)
		return mv
	}
	r := MannWhitney(m.Canary, m.Baseline) // delta > 0 means canary larger
	mv.PValue = r.P
	mv.CliffsDelta = r.CliffsDelta

	significant := r.P < cfg.Alpha && math.Abs(r.CliffsDelta) >= cfg.MinEffect
	worse := false
	switch mv.Direction {
	case LowerIsBetter:
		worse = r.CliffsDelta > 0
	case HigherIsBetter:
		worse = r.CliffsDelta < 0
	case Either:
		worse = true
	}
	if significant && worse {
		mv.Outcome = model.OutcomeFail
		mv.Reason = fmt.Sprintf("%s moved %s (median %s -> %s, %+.0f%%, p=%.3g, delta=%.2f)",
			m.Name, worseWord(mv.Direction, r.CliffsDelta), fmtNum(mv.BaselineMedian), fmtNum(mv.CanaryMedian), mv.PctChange, r.P, r.CliffsDelta)
		return mv
	}
	mv.Outcome = model.OutcomePass
	if significant {
		mv.Improved = true
		mv.Reason = fmt.Sprintf("%s improved (median %s -> %s, %+.0f%%, p=%.3g)", m.Name, fmtNum(mv.BaselineMedian), fmtNum(mv.CanaryMedian), mv.PctChange, r.P)
	} else {
		mv.Reason = fmt.Sprintf("%s unchanged vs baseline (p=%.3g, delta=%.2f)", m.Name, r.P, r.CliffsDelta)
	}
	return mv
}

func passConfidence(mv MetricVerdict) float64 {
	// Confident pass = high p (no evidence of shift) and small effect.
	return clamp(math.Max(mv.PValue, 1-math.Abs(mv.CliffsDelta)), 0, 1)
}

func worseWord(d Direction, delta float64) string {
	if delta > 0 {
		return "up"
	}
	return "down"
}

func maxAbsDelta(ms []MetricVerdict) float64 {
	var m float64
	for _, mv := range ms {
		if d := math.Abs(mv.CliffsDelta); d > m {
			m = d
		}
	}
	return m
}

func fmtNum(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e9 {
		return fmt.Sprintf("%.0f", f)
	}
	return fmt.Sprintf("%.3g", f)
}

func clamp(x, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, x))
}
