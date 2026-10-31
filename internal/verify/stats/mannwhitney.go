// Package stats implements the tier-1 statistical canary judge: a
// Mann-Whitney U comparison of canary vs baseline samples per metric,
// Kayenta-style, in Go (OPEN_QUESTIONS.md #7).
package stats

import (
	"math"
	"sort"
)

// MannWhitneyResult is the outcome of a two-sided Mann-Whitney U test.
type MannWhitneyResult struct {
	U float64 // U statistic for sample A
	// Z is the tie-corrected normal approximation of U.
	Z float64
	// P is the two-sided p-value from the normal approximation.
	P float64
	// CliffsDelta is the effect size in [-1, 1]: P(a > b) - P(a < b).
	// Positive means A tends to be larger than B.
	CliffsDelta float64
}

// MannWhitney compares samples a and b. It requires at least one sample in
// each; callers decide what "enough" is (see Judge). With tiny samples the
// normal approximation is rough, which is why Judge reports "unsure" below a
// minimum sample size rather than trusting the p-value.
func MannWhitney(a, b []float64) MannWhitneyResult {
	na, nb := float64(len(a)), float64(len(b))
	if na == 0 || nb == 0 {
		return MannWhitneyResult{P: 1}
	}

	type obs struct {
		v    float64
		from int // 0 = a, 1 = b
	}
	all := make([]obs, 0, len(a)+len(b))
	for _, v := range a {
		all = append(all, obs{v, 0})
	}
	for _, v := range b {
		all = append(all, obs{v, 1})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].v < all[j].v })

	// Midranks with tie correction term sum(t^3 - t).
	ranks := make([]float64, len(all))
	var tieTerm float64
	for i := 0; i < len(all); {
		j := i
		for j+1 < len(all) && all[j+1].v == all[i].v {
			j++
		}
		r := float64(i+j)/2 + 1 // 1-based midrank
		for k := i; k <= j; k++ {
			ranks[k] = r
		}
		if t := float64(j - i + 1); t > 1 {
			tieTerm += t*t*t - t
		}
		i = j + 1
	}

	var ra float64
	for i, o := range all {
		if o.from == 0 {
			ra += ranks[i]
		}
	}
	u := ra - na*(na+1)/2 // U for sample a
	n := na + nb
	mu := na * nb / 2
	sigma2 := na * nb / 12 * ((n + 1) - tieTerm/(n*(n-1)))
	res := MannWhitneyResult{U: u}
	// Cliff's delta: (#a>b - #a<b) / (na*nb). U counts a>b plus half the ties,
	// so delta = 2U/(na*nb) - 1.
	res.CliffsDelta = 2*u/(na*nb) - 1
	if sigma2 <= 0 {
		// All observations identical.
		res.P = 1
		return res
	}
	// Continuity correction.
	diff := u - mu
	switch {
	case diff > 0:
		diff -= 0.5
	case diff < 0:
		diff += 0.5
	}
	res.Z = diff / math.Sqrt(sigma2)
	res.P = 2 * normalSurvival(math.Abs(res.Z))
	if res.P > 1 {
		res.P = 1
	}
	return res
}

// normalSurvival is P(Z > z) for the standard normal distribution.
func normalSurvival(z float64) float64 {
	return 0.5 * math.Erfc(z/math.Sqrt2)
}

// Median returns the median of xs (0 for empty input).
func Median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	m := len(s) / 2
	if len(s)%2 == 1 {
		return s[m]
	}
	return (s[m-1] + s[m]) / 2
}
