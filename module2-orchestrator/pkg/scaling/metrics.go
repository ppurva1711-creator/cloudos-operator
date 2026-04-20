package scaling

import (
	"context"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

// MetricsCollector collects and exposes custom metrics for HPA
type MetricsCollector struct {
	log               *logrus.Entry
	redisClient       interface{} // redis.Client
	storageClient     interface{} // storage.Store
	updateInterval    time.Duration
	stopCh            chan struct{}
	queueDepthGauge   prometheus.Gauge
	activeTasksGauge  prometheus.Gauge
	failedTasksGauge  prometheus.Gauge
	avgDurationGauge  prometheus.Gauge
}

// NewMetricsCollector creates a new metrics collector
func NewMetricsCollector(log *logrus.Entry, redisClient, storageClient interface{}, updateInterval time.Duration) *MetricsCollector {
	// Create Prometheus metrics
	queueDepthGauge := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "current_queue_depth",
			Help: "Current depth of the task queue in Redis",
		},
	)

	activeTasksGauge := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "active_task_count",
			Help: "Number of currently executing tasks",
		},
	)

	failedTasksGauge := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "failed_task_count",
			Help: "Number of failed tasks in the last hour",
		},
	)

	avgDurationGauge := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "avg_task_duration_seconds",
			Help: "Average task execution duration in seconds",
		},
	)

	return &MetricsCollector{
		log:              log.WithField("component", "metrics"),
		redisClient:      redisClient,
		storageClient:    storageClient,
		updateInterval:   updateInterval,
		stopCh:           make(chan struct{}),
		queueDepthGauge:  queueDepthGauge,
		activeTasksGauge: activeTasksGauge,
		failedTasksGauge: failedTasksGauge,
		avgDurationGauge: avgDurationGauge,
	}
}

// Register registers all metrics with Prometheus
func (mc *MetricsCollector) Register() error {
	metrics := []prometheus.Collector{
		mc.queueDepthGauge,
		mc.activeTasksGauge,
		mc.failedTasksGauge,
		mc.avgDurationGauge,
	}

	for _, m := range metrics {
		if err := prometheus.Register(m); err != nil {
			// Metric already registered is acceptable
			if _, ok := err.(prometheus.AlreadyRegisteredError); !ok {
				mc.log.Errorf("failed to register metric: %v", err)
				return err
			}
		}
	}

	mc.log.Info("all metrics registered successfully")
	return nil
}

// Start begins the background metrics collection goroutine
func (mc *MetricsCollector) Start(ctx context.Context) {
	go mc.collectMetrics(ctx)
	mc.log.Info("metrics collector started")
}

// Stop stops the metrics collection goroutine
func (mc *MetricsCollector) Stop() {
	close(mc.stopCh)
	mc.log.Info("metrics collector stopped")
}

// collectMetrics periodically updates metrics
func (mc *MetricsCollector) collectMetrics(ctx context.Context) {
	ticker := time.NewTicker(mc.updateInterval)
	defer ticker.Stop()

	// Collect immediately on start
	mc.updateMetrics(ctx)

	for {
		select {
		case <-ctx.Done():
			mc.log.Debug("metrics collection context cancelled")
			return
		case <-mc.stopCh:
			mc.log.Debug("metrics collection stop signal received")
			return
		case <-ticker.C:
			mc.updateMetrics(ctx)
		}
	}
}

// updateMetrics updates all metric values
func (mc *MetricsCollector) updateMetrics(ctx context.Context) {
	// Reset metrics to valid default values if collection fails
	mc.queueDepthGauge.Set(0)
	mc.activeTasksGauge.Set(0)
	mc.failedTasksGauge.Set(0)
	mc.avgDurationGauge.Set(0)

	// Queue depth from Redis
	if queueDepth, err := mc.getQueueDepth(ctx); err == nil {
		mc.queueDepthGauge.Set(float64(queueDepth))
	} else {
		mc.log.WithError(err).Warn("failed to get queue depth from Redis")
	}

	// Active task count from storage
	if activeTasks, err := mc.getActiveTasks(ctx); err == nil {
		mc.activeTasksGauge.Set(float64(activeTasks))
	} else {
		mc.log.WithError(err).Warn("failed to get active task count")
	}

	// Failed task count from storage (last hour)
	if failedTasks, err := mc.getFailedTasks(ctx); err == nil {
		mc.failedTasksGauge.Set(float64(failedTasks))
	} else {
		mc.log.WithError(err).Warn("failed to get failed task count")
	}

	// Average task duration from storage
	if avgDuration, err := mc.getAvgDuration(ctx); err == nil {
		mc.avgDurationGauge.Set(avgDuration)
	} else {
		mc.log.WithError(err).Warn("failed to get average task duration")
	}
}

// getQueueDepth retrieves the current queue depth from Redis
func (mc *MetricsCollector) getQueueDepth(ctx context.Context) (int64, error) {
	// TODO: Implement Redis queue depth retrieval
	// For now, return 0 as placeholder
	// Example: return mc.redisClient.LLen(ctx, "task:queue").Val(), nil
	return 0, nil
}

// getActiveTasks retrieves the count of currently active tasks
func (mc *MetricsCollector) getActiveTasks(ctx context.Context) (int64, error) {
	// TODO: Implement active task count retrieval from storage
	// Query: SELECT COUNT(*) FROM task_executions WHERE status = 'Running'
	return 0, nil
}

// getFailedTasks retrieves the count of failed tasks in the last hour
func (mc *MetricsCollector) getFailedTasks(ctx context.Context) (int64, error) {
	// TODO: Implement failed task count retrieval from storage
	// Query: SELECT COUNT(*) FROM task_executions
	//        WHERE status = 'Failed' AND completed_at > NOW() - INTERVAL '1 hour'
	return 0, nil
}

// getAvgDuration retrieves the average task duration in seconds
func (mc *MetricsCollector) getAvgDuration(ctx context.Context) (float64, error) {
	// TODO: Implement average duration retrieval from storage
	// Query: SELECT AVG(duration_seconds) FROM task_executions
	//        WHERE status IN ('Completed', 'Failed') AND completed_at > NOW() - INTERVAL '1 hour'
	return 0, nil
}

// ServeMetrics registers the Prometheus metrics handler
func ServeMetrics(port string) {
	http.Handle("/metrics", promhttp.Handler())
	http.ListenAndServe(port, nil)
}

// GetQueueDepthMetric returns the queue depth gauge
func (mc *MetricsCollector) GetQueueDepthMetric() prometheus.Gauge {
	return mc.queueDepthGauge
}

// GetActiveTasksMetric returns the active tasks gauge
func (mc *MetricsCollector) GetActiveTasksMetric() prometheus.Gauge {
	return mc.activeTasksGauge
}

// GetFailedTasksMetric returns the failed tasks gauge
func (mc *MetricsCollector) GetFailedTasksMetric() prometheus.Gauge {
	return mc.failedTasksGauge
}

// GetAvgDurationMetric returns the average duration gauge
func (mc *MetricsCollector) GetAvgDurationMetric() prometheus.Gauge {
	return mc.avgDurationGauge
}
