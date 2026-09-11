// Package telemetry exposes Prometheus metrics for the /metrics endpoint.
package telemetry

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	registry *prometheus.Registry

	requests    *prometheus.CounterVec
	reqDuration *prometheus.HistogramVec

	ingestTotal    *prometheus.CounterVec
	ingestDuration prometheus.Histogram

	dispatchQueued    *prometheus.CounterVec
	dispatchCompleted *prometheus.CounterVec
	rulesFired        *prometheus.CounterVec

	wsConnections prometheus.GaugeFunc
}

// New builds the registry. connFn reports live WebSocket connections.
func New(connFn func() float64) *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	m := &Metrics{
		registry: reg,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "marblejar_http_requests_total",
			Help: "Total HTTP requests by method, route and status.",
		}, []string{"method", "route", "status"}),
		reqDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "marblejar_http_request_duration_seconds",
			Help:    "HTTP request latency.",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
		}, []string{"method", "route"}),
		ingestTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "marblejar_marbles_ingested_total",
			Help: "Marble ingestion outcomes.",
		}, []string{"outcome"}),
		ingestDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "marblejar_ingest_duration_seconds",
			Help:    "End-to-end marble ingestion latency.",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
		}),
		dispatchQueued: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "marblejar_dispatches_queued_total",
			Help: "Dispatch intents published by integration type.",
		}, []string{"integration"}),
		dispatchCompleted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "marblejar_dispatches_completed_total",
			Help: "Dispatch outcomes reported by workers.",
		}, []string{"integration", "status"}),
		rulesFired: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "marblejar_rules_fired_total",
			Help: "Auto-dispatch rules that matched a marble.",
		}, []string{"integration"}),
	}

	m.wsConnections = prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "marblejar_ws_connections",
		Help: "Live WebSocket queue-feed connections.",
	}, connFn)

	reg.MustRegister(m.requests, m.reqDuration, m.ingestTotal, m.ingestDuration,
		m.dispatchQueued, m.dispatchCompleted, m.rulesFired, m.wsConnections)
	return m
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

func (m *Metrics) ObserveRequest(method, route string, status int, d time.Duration) {
	m.requests.WithLabelValues(method, route, strconv.Itoa(status)).Inc()
	m.reqDuration.WithLabelValues(method, route).Observe(d.Seconds())
}

func (m *Metrics) IngestAccepted(d time.Duration) {
	m.ingestTotal.WithLabelValues("accepted").Inc()
	m.ingestDuration.Observe(d.Seconds())
}

func (m *Metrics) IngestRejected() { m.ingestTotal.WithLabelValues("rejected").Inc() }
func (m *Metrics) IngestFailed()   { m.ingestTotal.WithLabelValues("failed").Inc() }

func (m *Metrics) DispatchQueued(integration string) {
	m.dispatchQueued.WithLabelValues(integration).Inc()
}

func (m *Metrics) DispatchCompleted(integration, status string) {
	m.dispatchCompleted.WithLabelValues(integration, status).Inc()
}

func (m *Metrics) RuleFired(integration string) {
	m.rulesFired.WithLabelValues(integration).Inc()
}
