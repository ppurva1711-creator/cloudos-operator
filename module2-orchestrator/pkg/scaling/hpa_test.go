package scaling

import (
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func TestCalculateDesiredReplicas(t *testing.T) {
	log := logrus.NewEntry(logrus.New())
	calculator := NewHPACalculator(log, 2, 50, 10, 70.0, 80.0)

	tests := []struct {
		name           string
		queueDepth     int64
		expectedMinMax string // "min" or "normal" or "max"
		expectedCount  int32
		description    string
	}{
		{
			name:           "empty queue returns minimum",
			queueDepth:     0,
			expectedMinMax: "min",
			expectedCount:  2,
			description:    "queue=0 → replicas=2 (minimum)",
		},
		{
			name:           "10 items returns 1 adjusted to min",
			queueDepth:     10,
			expectedMinMax: "min",
			expectedCount:  2,
			description:    "queue=10 → replicas=1... adjusted to min=2",
		},
		{
			name:           "50 items returns 5",
			queueDepth:     50,
			expectedMinMax: "normal",
			expectedCount:  5,
			description:    "queue=50 → replicas=5",
		},
		{
			name:           "500 items returns maximum",
			queueDepth:     500,
			expectedMinMax: "max",
			expectedCount:  50,
			description:    "queue=500 → replicas=50 (maximum)",
		},
		{
			name:           "100 items returns 10",
			queueDepth:     100,
			expectedMinMax: "normal",
			expectedCount:  10,
			description:    "queue=100 → replicas=10",
		},
		{
			name:           "5 items returns minimum",
			queueDepth:     5,
			expectedMinMax: "min",
			expectedCount:  2,
			description:    "queue=5 → replicas=1... adjusted to min=2",
		},
		{
			name:           "999 items capped at max",
			queueDepth:     999,
			expectedMinMax: "max",
			expectedCount:  50,
			description:    "queue=999 → replicas=50 (maximum)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculator.CalculateDesiredReplicas(tt.queueDepth)
			assert.Equal(t, tt.expectedCount, result, tt.description)
		})
	}
}

func TestShouldScaleUp(t *testing.T) {
	log := logrus.NewEntry(logrus.New())
	calculator := NewHPACalculator(log, 2, 50, 10, 70.0, 80.0)

	tests := []struct {
		name     string
		metrics  MetricsSnapshot
		expected bool
		reason   string
	}{
		{
			name: "scale up when CPU exceeds threshold",
			metrics: MetricsSnapshot{
				QueueDepth:      10,
				ActiveTasks:     5,
				CurrentReplicas: 2,
				CurrentCPU:      75.0,
				TargetCPU:       70.0,
			},
			expected: true,
			reason:   "CPU at 75% exceeds 70% threshold",
		},
		{
			name: "scale up when memory exceeds threshold",
			metrics: MetricsSnapshot{
				QueueDepth:      10,
				ActiveTasks:     5,
				CurrentReplicas: 2,
				CurrentMemory:   85.0,
				TargetMemory:    80.0,
			},
			expected: true,
			reason:   "Memory at 85% exceeds 80% threshold",
		},
		{
			name: "scale up when queue depth requires more replicas",
			metrics: MetricsSnapshot{
				QueueDepth:      100,
				ActiveTasks:     5,
				CurrentReplicas: 2,
				CurrentCPU:      30.0,
				CurrentMemory:   40.0,
			},
			expected: true,
			reason:   "Queue depth of 100 requires 10 replicas, current is 2",
		},
		{
			name: "no scale up when metrics are normal",
			metrics: MetricsSnapshot{
				QueueDepth:      10,
				ActiveTasks:     5,
				CurrentReplicas: 2,
				CurrentCPU:      30.0,
				CurrentMemory:   40.0,
			},
			expected: false,
			reason:   "All metrics below thresholds",
		},
		{
			name: "no scale up with empty queue and low CPU/memory",
			metrics: MetricsSnapshot{
				QueueDepth:      0,
				ActiveTasks:     0,
				CurrentReplicas: 2,
				CurrentCPU:      10.0,
				CurrentMemory:   20.0,
			},
			expected: false,
			reason:   "No work queued, resources underutilized",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculator.ShouldScaleUp(tt.metrics)
			assert.Equal(t, tt.expected, result, tt.reason)
		})
	}
}

func TestShouldScaleDown(t *testing.T) {
	log := logrus.NewEntry(logrus.New())
	calculator := NewHPACalculator(log, 2, 50, 10, 70.0, 80.0)

	tests := []struct {
		name     string
		metrics  MetricsSnapshot
		expected bool
		reason   string
	}{
		{
			name: "no scale down when at minimum replicas",
			metrics: MetricsSnapshot{
				QueueDepth:      0,
				ActiveTasks:     0,
				CurrentReplicas: 2,
				CurrentCPU:      5.0,
				CurrentMemory:   10.0,
			},
			expected: false,
			reason:   "Already at minimum replicas (2)",
		},
		{
			name: "scale down when CPU is well below threshold",
			metrics: MetricsSnapshot{
				QueueDepth:      5,
				ActiveTasks:     1,
				CurrentReplicas: 10,
				CurrentCPU:      20.0, // Below 50% of 70% threshold
				CurrentMemory:   60.0,
			},
			expected: true,
			reason:   "CPU at 20% is well below 35% margin",
		},
		{
			name: "scale down when memory is well below threshold",
			metrics: MetricsSnapshot{
				QueueDepth:      5,
				ActiveTasks:     1,
				CurrentReplicas: 10,
				CurrentCPU:      50.0,
				CurrentMemory:   30.0, // Below 50% of 80% threshold
			},
			expected: true,
			reason:   "Memory at 30% is well below 40% margin",
		},
		{
			name: "scale down when queue depth allows fewer replicas",
			metrics: MetricsSnapshot{
				QueueDepth:      5,
				ActiveTasks:     1,
				CurrentReplicas: 20,
				CurrentCPU:      40.0,
				CurrentMemory:   50.0,
			},
			expected: true,
			reason:   "Queue depth of 5 only needs 1 replica, current is 20",
		},
		{
			name: "no scale down when scale up conditions met",
			metrics: MetricsSnapshot{
				QueueDepth:      100,
				ActiveTasks:     50,
				CurrentReplicas: 5,
				CurrentCPU:      75.0,
				CurrentMemory:   85.0,
			},
			expected: false,
			reason:   "Scale up conditions take priority",
		},
		{
			name: "no scale down when CPU is moderate",
			metrics: MetricsSnapshot{
				QueueDepth:      20,
				ActiveTasks:     5,
				CurrentReplicas: 10,
				CurrentCPU:      45.0, // Above margin but below threshold
				CurrentMemory:   50.0,
			},
			expected: false,
			reason:   "CPU at 45% is above margin (35%)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculator.ShouldScaleDown(tt.metrics)
			assert.Equal(t, tt.expected, result, tt.reason)
		})
	}
}

func TestMakeScalingDecision(t *testing.T) {
	log := logrus.NewEntry(logrus.New())
	calculator := NewHPACalculator(log, 2, 50, 10, 70.0, 80.0)

	tests := []struct {
		name        string
		metrics     MetricsSnapshot
		expectedAct string
		minReplicas int32
		maxReplicas int32
		reason      string
	}{
		{
			name: "scale up decision with high CPU",
			metrics: MetricsSnapshot{
				QueueDepth:      50,
				ActiveTasks:     20,
				CurrentReplicas: 2,
				CurrentCPU:      85.0,
				CurrentMemory:   75.0,
			},
			expectedAct: "scale-up",
			minReplicas: 2,
			maxReplicas: 50,
			reason:      "Should scale up due to high CPU and queue depth",
		},
		{
			name: "scale down decision with low load",
			metrics: MetricsSnapshot{
				QueueDepth:      0,
				ActiveTasks:     0,
				CurrentReplicas: 10,
				CurrentCPU:      15.0,
				CurrentMemory:   20.0,
			},
			expectedAct: "scale-down",
			minReplicas: 2,
			maxReplicas: 10,
			reason:      "Should scale down to minimum due to idle state",
		},
		{
			name: "no change decision with balanced metrics",
			metrics: MetricsSnapshot{
				QueueDepth:      25,
				ActiveTasks:     5,
				CurrentReplicas: 5,
				CurrentCPU:      45.0,
				CurrentMemory:   55.0,
			},
			expectedAct: "no-change",
			minReplicas: 5,
			maxReplicas: 5,
			reason:      "Should maintain current replicas when balanced",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := calculator.MakeScalingDecision(tt.metrics)
			assert.Equal(t, tt.expectedAct, decision.Action, tt.reason)
			assert.NotEmpty(t, decision.Reason, "decision should have a reason")
		})
	}
}

func TestHPACalculator_EdgeCases(t *testing.T) {
	log := logrus.NewEntry(logrus.New())

	t.Run("calculator with different min/max values", func(t *testing.T) {
		calc := NewHPACalculator(log, 1, 100, 5, 75.0, 85.0)
		
		// With itemsPerPod=5, queue of 5 should need 1 replica (not adjusted to min)
		result := calc.CalculateDesiredReplicas(5)
		assert.Equal(t, int32(1), result)
		
		// With itemsPerPod=5, queue of 1000 should be capped at 100
		result = calc.CalculateDesiredReplicas(1000)
		assert.Equal(t, int32(100), result)
	})

	t.Run("calculator with high items per pod", func(t *testing.T) {
		calc := NewHPACalculator(log, 2, 50, 100, 70.0, 80.0)
		
		// 100 items with itemsPerPod=100 should need 1, adjusted to min=2
		result := calc.CalculateDesiredReplicas(100)
		assert.Equal(t, int32(2), result)
		
		// 1000 items with itemsPerPod=100 should need 10
		result = calc.CalculateDesiredReplicas(1000)
		assert.Equal(t, int32(10), result)
	})
}

func TestScalingDecision_Consistency(t *testing.T) {
	log := logrus.NewEntry(logrus.New())
	calculator := NewHPACalculator(log, 2, 50, 10, 70.0, 80.0)

	// Test that multiple calls with same metrics produce consistent results
	metrics := MetricsSnapshot{
		QueueDepth:      100,
		ActiveTasks:     10,
		CurrentReplicas: 5,
		CurrentCPU:      60.0,
		CurrentMemory:   65.0,
	}

	decision1 := calculator.MakeScalingDecision(metrics)
	decision2 := calculator.MakeScalingDecision(metrics)

	assert.Equal(t, decision1.Action, decision2.Action)
	assert.Equal(t, decision1.DesiredReplicas, decision2.DesiredReplicas)
}

func TestCalculateDesiredReplicas_Rounding(t *testing.T) {
	log := logrus.NewEntry(logrus.New())
	calculator := NewHPACalculator(log, 2, 50, 3, 70.0, 80.0) // itemsPerPod=3

	tests := []struct {
		queueDepth int64
		expected   int32
		reason     string
	}{
		{1, 2, "1 item with itemsPerPod=3 should round up to 1, adjusted to min=2"},
		{2, 2, "2 items with itemsPerPod=3 should round up to 1, adjusted to min=2"},
		{3, 2, "3 items with itemsPerPod=3 should be exactly 1, adjusted to min=2"},
		{4, 2, "4 items with itemsPerPod=3 should round up to 2"},
		{6, 2, "6 items with itemsPerPod=3 should be exactly 2"},
		{7, 3, "7 items with itemsPerPod=3 should round up to 3"},
		{9, 3, "9 items with itemsPerPod=3 should be exactly 3"},
	}

	for _, tt := range tests {
		t.Run(tt.reason, func(t *testing.T) {
			result := calculator.CalculateDesiredReplicas(tt.queueDepth)
			assert.Equal(t, tt.expected, result, tt.reason)
		})
	}
}
