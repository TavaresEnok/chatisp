// Package metrics exposes low-cardinality Prometheus series on a private registry.
package metrics

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"chatisp/internal/platform/version"
)

// Metrics records process, HTTP, pool, WebSocket, River and operational series.
// Labels stay on closed sets. Nil methods are no-ops so tests can omit it.
type Metrics struct {
	registry  *prometheus.Registry
	requests  *prometheus.CounterVec
	duration  *prometheus.HistogramVec
	erp       *prometheus.CounterVec
	websocket prometheus.Gauge

	mu         sync.Mutex
	jobResults *prometheus.CounterVec
	pools      *poolCollector
	river      *riverCollector
	ops        *opsCollector
}

// New registers process, runtime and HTTP series on an isolated registry.
func New() *Metrics {
	registry := prometheus.NewRegistry()
	metrics := &Metrics{
		registry: registry,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "chatisp_http_requests_total",
			Help: "Total de requisições HTTP por método, rota e status.",
		}, []string{"method", "route", "status"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "chatisp_http_request_duration_seconds",
			Help:    "Duração das requisições HTTP por método e rota.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route"}),
		erp: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "chatisp_erp_connector_calls_total",
			Help: "Chamadas ao conector de ERP por conector e resultado.",
		}, []string{"connector", "result"}),
		websocket: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "chatisp_websocket_connections",
			Help: "Conexões WebSocket do painel aceitas e ainda abertas neste processo.",
		}),
	}
	info := prometheus.NewGauge(prometheus.GaugeOpts{
		Name:        "chatisp_build_info",
		Help:        "Informação de build. O valor é sempre 1.",
		ConstLabels: prometheus.Labels{"version": version.Version},
	})
	info.Set(1)
	registry.MustRegister(
		metrics.requests,
		metrics.duration,
		metrics.erp,
		metrics.websocket,
		info,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return metrics
}

// Handler serves GET /metrics from the isolated registry.
func (m *Metrics) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{}))
	return mux
}

// Observe records one HTTP request. Route values that are not templates are collapsed.
func (m *Metrics) Observe(method, route string, status int, elapsed time.Duration) {
	if m == nil {
		return
	}
	method = sanitizeMethod(method)
	route = sanitizeRoute(route)
	code := sanitizeStatus(status)
	m.requests.WithLabelValues(method, route, code).Inc()
	m.duration.WithLabelValues(method, route).Observe(elapsed.Seconds())
}

// ObserveERP counts one ERP connector result. Labels stay low cardinality.
func (m *Metrics) ObserveERP(connector, result string) {
	if m == nil || m.erp == nil {
		return
	}
	m.erp.WithLabelValues(sanitizeConnector(connector), sanitizeERPResult(result)).Inc()
}

func sanitizeConnector(connector string) string {
	if connector == "fake" {
		return connector
	}
	return "other"
}

func sanitizeERPResult(result string) string {
	switch result {
	case "ok", "unavailable", "hidden", "uncertain", "not_found", "denied", "conflict":
		return result
	default:
		return "other"
	}
}

// Gather returns the current series for tests and scrapes.
func (m *Metrics) Gather() ([]byte, error) {
	handler := promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
	recorder := &bodyRecorder{header: make(http.Header)}
	handler.ServeHTTP(recorder, mustRequest())
	return recorder.body, recorder.err
}

type bodyRecorder struct {
	header http.Header
	body   []byte
	status int
	err    error
}

func (b *bodyRecorder) Header() http.Header { return b.header }

func (b *bodyRecorder) Write(p []byte) (int, error) {
	b.body = append(b.body, p...)
	return len(p), nil
}

func (b *bodyRecorder) WriteHeader(status int) { b.status = status }

func mustRequest() *http.Request {
	request, err := http.NewRequest(http.MethodGet, "http://metrics.local/metrics", nil)
	if err != nil {
		panic(err)
	}
	return request
}

func sanitizeMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions:
		return method
	default:
		return "other"
	}
}

func sanitizeRoute(route string) string {
	if route == "" {
		return "unmatched"
	}
	if len(route) > 80 {
		return "unmatched"
	}
	for _, r := range route {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '/' || r == '_' || r == '-' || r == '{' || r == '}' || r == '.':
		default:
			return "unmatched"
		}
	}
	return route
}

func sanitizeStatus(status int) string {
	if status < 100 || status > 599 {
		return "0"
	}
	return strconv.Itoa(status)
}
