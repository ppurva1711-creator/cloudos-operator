package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// GatewayMetrics holds all Prometheus metrics for the API gateway
type GatewayMetrics struct {
	// Counters
	HTTPRequestsTotal prometheus.CounterVec

	// Histograms
	HTTPRequestDuration prometheus.HistogramVec

	// Gauges
	ActiveConnections prometheus.Gauge
}

// NewGatewayMetrics creates and registers all gateway metrics
func NewGatewayMetrics() *GatewayMetrics {
	return &GatewayMetrics{
		// HTTP Request Counter
		HTTPRequestsTotal: *promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "http_requests_total",
				Help: "Total number of HTTP requests processed",
			},
			[]string{"method", "endpoint", "status"},
		),

		// HTTP Request Duration Histogram
		HTTPRequestDuration: *promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name: "http_request_duration_seconds",
				Help: "Duration of HTTP requests in seconds",
				Buckets: []float64{
					0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0,
				},
			},
			[]string{"method", "endpoint"},
		),

		// Active Connections Gauge
		ActiveConnections: promauto.NewGauge(
			prometheus.GaugeOpts{
				Name: "active_connections",
				Help: "Number of active HTTP connections",
			},
		),
	}
}

// RecordRequest records an HTTP request with status and duration
func (m *GatewayMetrics) RecordRequest(method, endpoint, status string, duration float64) {
	m.HTTPRequestsTotal.WithLabelValues(method, endpoint, status).Inc()
	m.HTTPRequestDuration.WithLabelValues(method, endpoint).Observe(duration)
}

// IncActiveConnections increments the active connection count
func (m *GatewayMetrics) IncActiveConnections() {
	m.ActiveConnections.Inc()
}

// DecActiveConnections decrements the active connection count
func (m *GatewayMetrics) DecActiveConnections() {
	m.ActiveConnections.Dec()
}

// RecordHTTPRequest records a request (convenience method)
func (m *GatewayMetrics) RecordHTTPRequest(method, endpoint string, statusCode int, duration float64) {
	statusStr := formatStatusCode(statusCode)
	m.RecordRequest(method, endpoint, statusStr, duration)
}

// Helper function to format status codes
func formatStatusCode(statusCode int) string {
	switch {
	case statusCode >= 500:
		return "5xx"
	case statusCode >= 400:
		return "4xx"
	case statusCode >= 300:
		return "3xx"
	case statusCode >= 200:
		return "2xx"
	default:
		return "unknown"
	}
}
