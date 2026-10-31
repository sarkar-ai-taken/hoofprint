package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// KindPrometheusQueryRange runs a range query against the Prometheus the
// agent was pointed at with --prometheus-url. Metric evidence is collected
// this way so the control plane never reaches into the cluster (ADR-0004).
const KindPrometheusQueryRange = "prometheus.query_range"

// PromQueryRangeInput is the payload of prometheus.query_range.
type PromQueryRangeInput struct {
	Query string    `json:"query"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	Step  string    `json:"step"` // Go duration, e.g. "5s"
}

// PromSample is one (timestamp, value) point.
type PromSample struct {
	T time.Time `json:"t"`
	V float64   `json:"v"`
}

// PromSeries is one result series of a range query.
type PromSeries struct {
	Metric map[string]string `json:"metric"`
	Values []PromSample      `json:"values"`
}

// PromQueryRangeOutput is the result payload.
type PromQueryRangeOutput struct {
	Series []PromSeries `json:"series"`
}

// RegisterPrometheusHandler wires prometheus.query_range against baseURL.
func (a *Agent) RegisterPrometheusHandler(baseURL string, client *http.Client) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	base := strings.TrimRight(baseURL, "/")
	a.Handle(KindPrometheusQueryRange, func(ctx context.Context, payload json.RawMessage, logf func(string)) (json.RawMessage, error) {
		var in PromQueryRangeInput
		if err := json.Unmarshal(payload, &in); err != nil {
			return nil, fmt.Errorf("bad payload: %w", err)
		}
		if in.Query == "" || in.Start.IsZero() || in.End.IsZero() {
			return nil, fmt.Errorf("query, start and end are required")
		}
		step, err := time.ParseDuration(in.Step)
		if err != nil || step <= 0 {
			step = 15 * time.Second
		}
		out, err := promQueryRange(ctx, client, base, in.Query, in.Start, in.End, step)
		if err != nil {
			return nil, err
		}
		n := 0
		for _, s := range out.Series {
			n += len(s.Values)
		}
		logf(fmt.Sprintf("prometheus: %d series, %d samples for %q", len(out.Series), n, in.Query))
		return json.Marshal(out)
	})
}

func promQueryRange(ctx context.Context, client *http.Client, base, query string, start, end time.Time, step time.Duration) (*PromQueryRangeOutput, error) {
	q := url.Values{
		"query": {query},
		"start": {strconv.FormatFloat(float64(start.UnixNano())/1e9, 'f', 3, 64)},
		"end":   {strconv.FormatFloat(float64(end.UnixNano())/1e9, 'f', 3, 64)},
		"step":  {strconv.FormatFloat(step.Seconds(), 'f', -1, 64)},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/query_range?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prometheus request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	var raw struct {
		Status    string `json:"status"`
		ErrorType string `json:"errorType"`
		Error     string `json:"error"`
		Data      struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Values [][2]any          `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("prometheus: bad response (%d): %s", resp.StatusCode, truncateBytes(body, 200))
	}
	if raw.Status != "success" {
		return nil, fmt.Errorf("prometheus: %s: %s", raw.ErrorType, raw.Error)
	}
	if raw.Data.ResultType != "matrix" {
		return nil, fmt.Errorf("prometheus: expected matrix, got %q", raw.Data.ResultType)
	}
	out := &PromQueryRangeOutput{Series: []PromSeries{}}
	for _, r := range raw.Data.Result {
		s := PromSeries{Metric: r.Metric}
		for _, pair := range r.Values {
			ts, ok := pair[0].(float64)
			if !ok {
				continue
			}
			vs, ok := pair[1].(string)
			if !ok {
				continue
			}
			v, err := strconv.ParseFloat(vs, 64)
			if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
				continue // Prometheus emits NaN/Inf for empty rate windows; they carry no signal
			}
			s.Values = append(s.Values, PromSample{T: time.Unix(0, int64(ts*1e9)).UTC(), V: v})
		}
		out.Series = append(out.Series, s)
	}
	return out, nil
}

func truncateBytes(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
