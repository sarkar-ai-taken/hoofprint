# sample-app

A two-endpoint HTTP service with Prometheus metrics and injectable regressions.
It exists so Hoofprint has something realistic to judge.

| Knob | Env (set at deploy) | Runtime (`POST /admin/inject`) | Effect |
|---|---|---|---|
| Latency | `INJECT_LATENCY_MS` | `latency_ms` | adds N ms (±20% jitter) to every request |
| Errors | `INJECT_ERROR_RATE` | `error_rate` | fraction of requests returning 500 |
| Memory leak | `INJECT_LEAK_MB_PER_MIN` | `leak_mb_per_min` | retains N MB/min |
| CPU | `INJECT_CPU_SPIN_MS` | `cpu_spin_ms` | burns N ms CPU per request |

Metrics: `http_requests_total{route,code}`, `http_request_duration_seconds{route}`,
`http_requests_in_flight`, `sample_app_leak_bytes`.

Inject a regression the way a bad deploy would, by changing the pod template:

```sh
kubectl -n sample set env deployment/sample-app INJECT_LATENCY_MS=300 APP_VERSION=v2
```

Or at runtime without a deploy (useful for calibrating the judge):

```sh
kubectl -n sample port-forward svc/sample-app 8081:80 &
curl -X POST localhost:8081/admin/inject -d '{"latency_ms":300}'
```
