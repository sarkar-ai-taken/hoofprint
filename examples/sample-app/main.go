// sample-app is a small HTTP service with Prometheus metrics and knobs for
// injecting the regressions Hoofprint has to catch: latency, errors, memory
// growth. Knobs are set from env at startup (so a "bad" deploy is just a
// Deployment env change) or at runtime via POST /admin/inject.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Injection is the live fault configuration.
type Injection struct {
	// LatencyMS is added to every request.
	LatencyMS int `json:"latency_ms"`
	// ErrorRate in [0,1]: fraction of requests that return 500.
	ErrorRate float64 `json:"error_rate"`
	// LeakMBPerMin grows a retained buffer at this rate.
	LeakMBPerMin int `json:"leak_mb_per_min"`
	// CPUSpinMS burns CPU for this long per request.
	CPUSpinMS int `json:"cpu_spin_ms"`
}

var (
	mu       sync.RWMutex
	current  Injection
	leak     [][]byte
	requests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total", Help: "Requests by route and status.",
	}, []string{"route", "code"})
	latency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "http_request_duration_seconds", Help: "Request latency.",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
	}, []string{"route"})
	inflight  = promauto.NewGauge(prometheus.GaugeOpts{Name: "http_requests_in_flight", Help: "In-flight requests."})
	leakBytes = promauto.NewGauge(prometheus.GaugeOpts{Name: "sample_app_leak_bytes", Help: "Bytes retained by the leak injector."})
	version   = envOr("APP_VERSION", "dev")
	healthy   atomic.Bool
)

func main() {
	current = Injection{
		LatencyMS:    envInt("INJECT_LATENCY_MS", 0),
		ErrorRate:    envFloat("INJECT_ERROR_RATE", 0),
		LeakMBPerMin: envInt("INJECT_LEAK_MB_PER_MIN", 0),
		CPUSpinMS:    envInt("INJECT_CPU_SPIN_MS", 0),
	}
	healthy.Store(true)
	go leaker()

	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.Handler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		if !healthy.Load() {
			http.Error(w, "unhealthy", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("GET /admin/inject", func(w http.ResponseWriter, _ *http.Request) {
		mu.RLock()
		defer mu.RUnlock()
		writeJSON(w, current)
	})
	mux.HandleFunc("POST /admin/inject", func(w http.ResponseWriter, r *http.Request) {
		var in Injection
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		current = in
		if in.LeakMBPerMin == 0 {
			leak = nil
			leakBytes.Set(0)
		}
		mu.Unlock()
		log.Printf("injection updated: %+v", in)
		writeJSON(w, in)
	})
	mux.HandleFunc("GET /{$}", instrument("root", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "sample-app %s\n", version)
	}))
	mux.HandleFunc("GET /api/orders", instrument("orders", func(w http.ResponseWriter, _ *http.Request) {
		n := 1 + rand.Intn(5)
		orders := make([]map[string]any, n)
		for i := range orders {
			orders[i] = map[string]any{"id": rand.Intn(100000), "total_cents": 500 + rand.Intn(20000)}
		}
		writeJSON(w, map[string]any{"orders": orders, "version": version})
	}))
	mux.HandleFunc("GET /api/checkout", instrument("checkout", func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(time.Duration(10+rand.Intn(20)) * time.Millisecond) // baseline work
		writeJSON(w, map[string]any{"status": "ok", "version": version})
	}))

	addr := ":" + envOr("PORT", "8080")
	log.Printf("sample-app %s listening on %s, injection=%+v", version, addr, current)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func instrument(route string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		inflight.Inc()
		defer inflight.Dec()
		mu.RLock()
		inj := current
		mu.RUnlock()

		if inj.CPUSpinMS > 0 {
			spin(time.Duration(inj.CPUSpinMS) * time.Millisecond)
		}
		if inj.LatencyMS > 0 {
			// Jitter so the distribution looks real.
			time.Sleep(time.Duration(float64(inj.LatencyMS)*(0.8+0.4*rand.Float64())) * time.Millisecond)
		}
		code := http.StatusOK
		if inj.ErrorRate > 0 && rand.Float64() < inj.ErrorRate {
			code = http.StatusInternalServerError
			http.Error(w, "injected failure", code)
		} else {
			next(w, r)
		}
		requests.WithLabelValues(route, strconv.Itoa(code)).Inc()
		latency.WithLabelValues(route).Observe(time.Since(start).Seconds())
	}
}

func leaker() {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for range tick.C {
		mu.Lock()
		rate := current.LeakMBPerMin
		if rate > 0 {
			chunk := make([]byte, rate*1024*1024/60)
			for i := range chunk {
				chunk[i] = byte(i) // touch pages so RSS actually grows
			}
			leak = append(leak, chunk)
			var total int
			for _, c := range leak {
				total += len(c)
			}
			leakBytes.Set(float64(total))
		}
		mu.Unlock()
	}
}

func spin(d time.Duration) {
	end := time.Now().Add(d)
	x := 0.0
	for time.Now().Before(end) {
		x += rand.Float64()
	}
	_ = x
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil {
		return v
	}
	return def
}

func envFloat(k string, def float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(k), 64); err == nil {
		return v
	}
	return def
}
