package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPrometheusQueryRange(t *testing.T) {
	var gotQuery, gotStep string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query_range" {
			http.NotFound(w, r)
			return
		}
		gotQuery = r.URL.Query().Get("query")
		gotStep = r.URL.Query().Get("step")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[
			{"metric":{"pod":"a"},"values":[[1700000000,"0.12"],[1700000005,"0.15"],[1700000010,"NaN"]]},
			{"metric":{"pod":"b"},"values":[[1700000000,"0.20"]]}]}}`))
	}))
	defer srv.Close()

	a := New(Config{Cluster: "c"})
	a.RegisterPrometheusHandler(srv.URL, srv.Client())
	in, _ := json.Marshal(PromQueryRangeInput{Query: "up", Start: time.Unix(1700000000, 0), End: time.Unix(1700000060, 0), Step: "5s"})
	var logs []string
	raw, err := a.handlers[KindPrometheusQueryRange](context.Background(), in, func(s string) { logs = append(logs, s) })
	if err != nil {
		t.Fatal(err)
	}
	var out PromQueryRangeOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if gotQuery != "up" || gotStep != "5" {
		t.Fatalf("query=%q step=%q", gotQuery, gotStep)
	}
	if len(out.Series) != 2 || len(out.Series[0].Values) != 2 || out.Series[0].Values[1].V != 0.15 || out.Series[1].Metric["pod"] != "b" {
		t.Fatalf("parsed: %+v", out)
	}
	if out.Series[0].Values[0].T.Unix() != 1700000000 {
		t.Fatalf("timestamp: %v", out.Series[0].Values[0].T)
	}
	if len(logs) != 1 {
		t.Fatalf("logs: %v", logs)
	}

	// Error responses surface as handler errors.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"status":"error","errorType":"bad_data","error":"parse error"}`))
	}))
	defer bad.Close()
	a.RegisterPrometheusHandler(bad.URL, bad.Client())
	if _, err := a.handlers[KindPrometheusQueryRange](context.Background(), in, func(string) {}); err == nil {
		t.Fatal("expected error from prometheus error response")
	}
}
