package scaling

import (
	"fmt"

	"github.com/sirupsen/logrus"
)

// MetricsSnapshot represents a snapshot of current metrics
type MetricsSnapshot struct {
	QueueDepth       int64
	ActiveTasks      int64
	FailedTasks      int64
	AvgDurationSecs  float64
	CurrentReplicas  int32
	CurrentCPU       float64
	CurrentMemory    float64
	TargetCPU        float64
	TargetMemory     float64
}

// ScalingDecision represents the result of a scaling calculation
type ScalingDecision struct {
	DesiredReplicas   int32
	Action            string // "scale-up", "scale-down", "no-change"
	Reason            string
	QueueBasedReplicas int32
}

// HPACalculator handles HPA decision making
type HPACalculator struct {
	log           *logrus.Entry
	minReplicas   int32
	maxReplicas   int32
	itemsPerPod   int64
	cpuThreshold  float64
	memThreshold  float64
}

// NewHPACalculator creates a new HPA calculator
func NewHPACalculator(log *logrus.Entry, minReplicas, maxReplicas int32, itemsPerPod int64, cpuThreshold, memThreshold float64) *HPACalculator {
	return &HPACalculator{
		log:           log.WithField("component", "hpa-calculator"),
		minReplicas:   minReplicas,
		maxReplicas:   maxReplicas,
		itemsPerPod:   itemsPerPod,
		cpuThreshold:  cpuThreshold,
		memThreshold:  memThreshold,
	}
}

// CalculateDesiredReplicas calculates the desired number of replicas based on queue depth
//
// Rule: 1 pod per itemsPerPod queue items
// Constraints: Min minReplicas, Max maxReplicas
func (hc *HPACalculator) CalculateDesiredReplicas(queueDepth int64) int32 {
	if queueDepth == 0 {
		return hc.minReplicas
	}

	// Calculate replicas needed: round up to nearest integer
	needed := (queueDepth + hc.itemsPerPod - 1) / hc.itemsPerPod

	// Apply constraints
	if needed < int64(hc.minReplicas) {
		return hc.minReplicas
	}
	if needed > int64(hc.maxReplicas) {
		return hc.maxReplicas
	}

	return int32(needed)
}

// ShouldScaleUp determines if the system should scale up based on metrics
func (hc *HPACalculator) ShouldScaleUp(metrics MetricsSnapshot) bool {
	// Scale up if CPU exceeds threshold
	if metrics.CurrentCPU > hc.cpuThreshold {
		hc.log.WithFields(logrus.Fields{
			"current_cpu": metrics.CurrentCPU,
			"threshold":   hc.cpuThreshold,
		}).Debug("scale-up triggered by CPU threshold")
		return true
	}

	// Scale up if memory exceeds threshold
	if metrics.CurrentMemory > hc.memThreshold {
		hc.log.WithFields(logrus.Fields{
			"current_memory": metrics.CurrentMemory,
			"threshold":      hc.memThreshold,
		}).Debug("scale-up triggered by memory threshold")
		return true
	}

	// Scale up if queue depth requires more replicas
	desiredFromQueue := hc.CalculateDesiredReplicas(metrics.QueueDepth)
	if desiredFromQueue > metrics.CurrentReplicas {
		hc.log.WithFields(logrus.Fields{
			"queue_depth":       metrics.QueueDepth,
			"current_replicas":  metrics.CurrentReplicas,
			"desired_replicas":  desiredFromQueue,
		}).Debug("scale-up triggered by queue depth")
		return true
	}

	return false
}

// ShouldScaleDown determines if the system should scale down based on metrics
func (hc *HPACalculator) ShouldScaleDown(metrics MetricsSnapshot) bool {
	// Don't scale down below minimum replicas
	if metrics.CurrentReplicas <= hc.minReplicas {
		return false
	}

	// Scale up has priority over scale down
	if hc.ShouldScaleUp(metrics) {
		return false
	}

	// Calculate margins for stability
	cpuMargin := hc.cpuThreshold * 0.5   // 50% of threshold
	memMargin := hc.memThreshold * 0.5   // 50% of threshold

	// Scale down if CPU is well below threshold - always scale down when CPU is very low
	if metrics.CurrentCPU < cpuMargin {
		hc.log.WithFields(logrus.Fields{
			"current_cpu":  metrics.CurrentCPU,
			"cpu_margin":   cpuMargin,
		}).Debug("scale-down triggered by low CPU utilization")
		return true
	}

	// Scale down if memory is well below threshold - always scale down when memory is very low
	if metrics.CurrentMemory < memMargin {
		hc.log.WithFields(logrus.Fields{
			"current_memory": metrics.CurrentMemory,
			"mem_margin":     memMargin,
		}).Debug("scale-down triggered by low memory utilization")
		return true
	}

	// Scale down based on queue depth if CPU is low enough (under 1.2x margin)
	// This prevents scaling down when CPU/memory are moderately utilized
	cpuThresholdForQueueScaling := hc.cpuThreshold * 0.6 // 60% of CPU threshold
	
	desiredFromQueue := hc.CalculateDesiredReplicas(metrics.QueueDepth)
	if desiredFromQueue < metrics.CurrentReplicas && metrics.CurrentCPU < cpuThresholdForQueueScaling {
		hc.log.WithFields(logrus.Fields{
			"queue_depth":                  metrics.QueueDepth,
			"current_replicas":             metrics.CurrentReplicas,
			"desired_replicas":             desiredFromQueue,
			"cpu_queue_scale_threshold":    cpuThresholdForQueueScaling,
			"current_cpu":                  metrics.CurrentCPU,
		}).Debug("scale-down triggered by low queue depth with low CPU")
		return true
	}

	return false
}

// MakeScalingDecision makes a comprehensive scaling recommendation
func (hc *HPACalculator) MakeScalingDecision(metrics MetricsSnapshot) ScalingDecision {
	decision := ScalingDecision{
		QueueBasedReplicas: hc.CalculateDesiredReplicas(metrics.QueueDepth),
	}

	// Determine scaling action
	if hc.ShouldScaleUp(metrics) {
		decision.DesiredReplicas = decision.QueueBasedReplicas

		// Also consider CPU/memory based scaling if queue-based is lower
		cpuBased := hc.calculateFromCPU(metrics)
		memBased := hc.calculateFromMemory(metrics)

		if cpuBased > decision.DesiredReplicas {
			decision.DesiredReplicas = cpuBased
			decision.Reason = fmt.Sprintf("CPU-based scaling (%.1f%% > %.1f%%)", metrics.CurrentCPU, hc.cpuThreshold)
		} else if memBased > decision.DesiredReplicas {
			decision.DesiredReplicas = memBased
			decision.Reason = fmt.Sprintf("Memory-based scaling (%.1f%% > %.1f%%)", metrics.CurrentMemory, hc.memThreshold)
		} else {
			decision.Reason = fmt.Sprintf("Queue-based scaling (depth: %d items)", metrics.QueueDepth)
		}

		decision.Action = "scale-up"
	} else if hc.ShouldScaleDown(metrics) {
		// Scale down to at least the queue-based requirement
		decision.DesiredReplicas = decision.QueueBasedReplicas
		if decision.DesiredReplicas < hc.minReplicas {
			decision.DesiredReplicas = hc.minReplicas
		}
		decision.Action = "scale-down"
		decision.Reason = fmt.Sprintf("Scaling down from %d to %d replicas (queue: %d items)", 
			metrics.CurrentReplicas, decision.DesiredReplicas, metrics.QueueDepth)
	} else {
		decision.DesiredReplicas = metrics.CurrentReplicas
		decision.Action = "no-change"
		decision.Reason = "Metrics within acceptable ranges"
	}

	return decision
}

// calculateFromCPU calculates desired replicas based on CPU usage
func (hc *HPACalculator) calculateFromCPU(metrics MetricsSnapshot) int32 {
	if metrics.CurrentCPU == 0 {
		return hc.minReplicas
	}
	ratio := metrics.CurrentCPU / hc.cpuThreshold
	desired := int32(float64(metrics.CurrentReplicas) * ratio)
	if desired < hc.minReplicas {
		return hc.minReplicas
	}
	if desired > hc.maxReplicas {
		return hc.maxReplicas
	}
	return desired
}

// calculateFromMemory calculates desired replicas based on memory usage
func (hc *HPACalculator) calculateFromMemory(metrics MetricsSnapshot) int32 {
	if metrics.CurrentMemory == 0 {
		return hc.minReplicas
	}
	ratio := metrics.CurrentMemory / hc.memThreshold
	desired := int32(float64(metrics.CurrentReplicas) * ratio)
	if desired < hc.minReplicas {
		return hc.minReplicas
	}
	if desired > hc.maxReplicas {
		return hc.maxReplicas
	}
	return desired
}
