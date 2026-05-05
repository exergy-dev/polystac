// Package observability owns PolyStac's logging, metrics, and tracing
// wiring. Importers are: internal/app (constructs everything), internal/
// server (middleware), and the backend packages (recording per-call
// timings).
//
// Three subsystems:
//
//   - slog handler (json or text) — see logger.go
//   - Prometheus metrics + HTTP middleware — see metrics.go
//   - OpenTelemetry tracer setup — see tracing.go
package observability

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics is the PolyStac metric registry. Construct via NewMetrics; the
// returned struct is safe for concurrent use.
//
// SDD §13: polystac_request_duration_seconds, polystac_repository_duration_
// seconds, polystac_backend_pool_in_use are the load-bearing names.
type Metrics struct {
	Registry             *prometheus.Registry
	RequestDuration      *prometheus.HistogramVec
	RepositoryDuration   *prometheus.HistogramVec
	BackendPoolInUse     *prometheus.GaugeVec
	HookInvocations      *prometheus.CounterVec
}

// NewMetrics constructs the metric registry and register the standard
// metric set. Caller exposes Registry on /metrics via Handler().
func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()
	m := &Metrics{
		Registry: reg,
		RequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "polystac_request_duration_seconds",
			Help:    "HTTP request duration by route, status, and configured backend.",
			Buckets: prometheus.DefBuckets,
		}, []string{"route", "method", "status", "backend"}),
		RepositoryDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "polystac_repository_duration_seconds",
			Help:    "Repository call duration by method and backend.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "backend", "status"}),
		BackendPoolInUse: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "polystac_backend_pool_in_use",
			Help: "In-use connections in the backend's pool.",
		}, []string{"backend"}),
		HookInvocations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "polystac_hook_invocations_total",
			Help: "Pre/post hook invocations by phase, name, and outcome.",
		}, []string{"phase", "name", "outcome"}),
	}
	reg.MustRegister(m.RequestDuration, m.RepositoryDuration, m.BackendPoolInUse, m.HookInvocations)
	reg.MustRegister(prometheus.NewGoCollector())
	reg.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	return m
}

// Handler returns the http.Handler that serves /metrics in the
// Prometheus text exposition format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{})
}

// HTTPMiddleware records request duration. Pass the configured backend
// name so labels are attributed correctly.
func (m *Metrics) HTTPMiddleware(backend string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := &statusRecorder{ResponseWriter: w, status: 200}
			next.ServeHTTP(ww, r)
			route := routePattern(r)
			m.RequestDuration.WithLabelValues(route, r.Method, strconv.Itoa(ww.status), backend).
				Observe(time.Since(start).Seconds())
		})
	}
}

// ObserveRepository records a repository-call timing.
func (m *Metrics) ObserveRepository(method, backend string, err error, dur time.Duration) {
	status := "ok"
	if err != nil {
		status = "error"
	}
	m.RepositoryDuration.WithLabelValues(method, backend, status).Observe(dur.Seconds())
}

// routePattern collapses dynamic path segments into the registered
// pattern so metric cardinality stays bounded. Falls back to the literal
// path on Go versions / muxes that don't expose a pattern.
func routePattern(r *http.Request) string {
	if pat := r.Pattern; pat != "" {
		// stdlib pattern is e.g. "GET /collections/{id}" — strip the method.
		for i := 0; i < len(pat); i++ {
			if pat[i] == ' ' {
				return pat[i+1:]
			}
		}
		return pat
	}
	return r.URL.Path
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}
