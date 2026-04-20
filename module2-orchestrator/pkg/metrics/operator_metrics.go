package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// OperatorMetrics holds all Prometheus metrics for the CloudTask operator
type OperatorMetrics struct {
	// Counters
	CloudTaskCreatedTotal    prometheus.CounterVec
	CloudTaskCompletedTotal  prometheus.CounterVec
	CloudTaskFailedTotal     prometheus.CounterVec
	PodRestartsTotal         prometheus.CounterVec

	// Histograms
	CloudTaskDuration        prometheus.HistogramVec

	// Gauges
	QueueDepth               prometheus.Gauge
}

// NewOperatorMetrics creates and registers all operator metrics
func NewOperatorMetrics() *OperatorMetrics {
	return &OperatorMetrics{
		// CloudTask Creation Counter
		CloudTaskCreatedTotal: *promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "cloudtask_created_total",
				Help: "Total number of CloudTasks created",
			},
			[]string{"namespace", "tenant"},
		),

		// CloudTask Completion Counter
		CloudTaskCompletedTotal: *promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "cloudtask_completed_total",
				Help: "Total number of CloudTasks completed successfully",
			},
			[]string{"namespace", "tenant"},
		),

		// CloudTask Failure Counter
		CloudTaskFailedTotal: *promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "cloudtask_failed_total",
				Help: "Total number of CloudTasks that failed",
			},
			[]string{"namespace", "tenant", "reason"},
		),

		// CloudTask Duration Histogram
		CloudTaskDuration: *promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name: "cloudtask_duration_seconds",
				Help: "Duration of CloudTask execution in seconds",
				Buckets: []float64{1, 5, 10, 30, 60, 120, 300, 600, 1800, 3600},
			},
			[]string{"namespace", "tenant"},
		),

		// Queue Depth Gauge
		QueueDepth: promauto.NewGauge(
			prometheus.GaugeOpts{
				Name: "cloudtask_queue_depth",
				Help: "Current depth of the task queue",
			},
		),

		// Pod Restarts Counter
		PodRestartsTotal: *promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "pod_restarts_total",
				Help: "Total number of pod restarts",
			},
			[]string{"pod_name", "namespace"},
		),
	}
}

// RecordTaskCreated records a new task creation
func (m *OperatorMetrics) RecordTaskCreated(namespace, tenantID string) {
	m.CloudTaskCreatedTotal.WithLabelValues(namespace, tenantID).Inc()
}

// RecordTaskCompleted records a completed task
func (m *OperatorMetrics) RecordTaskCompleted(namespace, tenantID string) {
	m.CloudTaskCompletedTotal.WithLabelValues(namespace, tenantID).Inc()
}

// RecordTaskFailed records a failed task
func (m *OperatorMetrics) RecordTaskFailed(namespace, tenantID, reason string) {
	m.CloudTaskFailedTotal.WithLabelValues(namespace, tenantID, reason).Inc()
}

// RecordTaskDuration records task execution duration
func (m *OperatorMetrics) RecordTaskDuration(namespace, tenantID string, duration float64) {
	m.CloudTaskDuration.WithLabelValues(namespace, tenantID).Observe(duration)
}

// SetQueueDepth updates the current queue depth
func (m *OperatorMetrics) SetQueueDepth(depth int64) {
	m.QueueDepth.Set(float64(depth))
}

// RecordPodRestart records a pod restart
func (m *OperatorMetrics) RecordPodRestart(podName, namespace string) {
	m.PodRestartsTotal.WithLabelValues(podName, namespace).Inc()
}
