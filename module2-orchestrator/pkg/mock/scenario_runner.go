package mock

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// Scenario defines a test scenario configuration
type Scenario struct {
	Name           string
	Description    string
	DelayMS        int
	FailureRate    int
	InitialQueue   int
	DurationSecs   int
	TaskSubmitRate int // Tasks per second
}

// ScenarioRunner manages test scenarios
type ScenarioRunner struct {
	log              *logrus.Entry
	service          *SchedulerService
	currentScenario  *Scenario
	mu               sync.RWMutex
	stopChan         chan struct{}
	metrics          *ScenarioMetrics
}

// ScenarioMetrics tracks scenario execution metrics
type ScenarioMetrics struct {
	TotalSubmitted   int
	TotalCompleted   int
	TotalFailed      int
	AverageDuration  float64
	PeakQueueDepth   int32
	StartTime        time.Time
	EndTime          time.Time
	mu               sync.RWMutex
}

// NewScenarioRunner creates a new scenario runner
func NewScenarioRunner(log *logrus.Entry, service *SchedulerService) *ScenarioRunner {
	return &ScenarioRunner{
		log:     log.WithField("component", "scenario-runner"),
		service: service,
		stopChan: make(chan struct{}),
		metrics: &ScenarioMetrics{
			StartTime: time.Now(),
		},
	}
}

// PredefinedScenarios returns all available scenarios
func PredefinedScenarios() []Scenario {
	return []Scenario{
		{
			Name:           "normal",
			Description:    "All tasks succeed with minimal delay",
			DelayMS:        100,
			FailureRate:    0,
			InitialQueue:   0,
			DurationSecs:   30,
			TaskSubmitRate: 10,
		},
		{
			Name:           "slow-scheduler",
			Description:    "Scheduler has significant delay on all tasks",
			DelayMS:        2000,
			FailureRate:    0,
			InitialQueue:   0,
			DurationSecs:   60,
			TaskSubmitRate: 5,
		},
		{
			Name:           "high-failure",
			Description:    "50% of tasks fail randomly",
			DelayMS:        500,
			FailureRate:    50,
			InitialQueue:   0,
			DurationSecs:   30,
			TaskSubmitRate: 15,
		},
		{
			Name:           "queue-backlog",
			Description:    "Slow processing creates queue backlog",
			DelayMS:        3000,
			FailureRate:    0,
			InitialQueue:   100,
			DurationSecs:   45,
			TaskSubmitRate: 20,
		},
		{
			Name:           "resource-contention",
			Description:    "Limited resources force pod scaling constraints",
			DelayMS:        800,
			FailureRate:    10,
			InitialQueue:   50,
			DurationSecs:   40,
			TaskSubmitRate: 12,
		},
		{
			Name:           "stress-test",
			Description:    "High volume stress test",
			DelayMS:        200,
			FailureRate:    5,
			InitialQueue:   200,
			DurationSecs:   60,
			TaskSubmitRate: 50,
		},
	}
}

// RunScenario runs a predefined scenario by name
func (sr *ScenarioRunner) RunScenario(name string) error {
	scenario := sr.findScenario(name)
	if scenario == nil {
		return fmt.Errorf("scenario not found: %s", name)
	}

	return sr.RunCustomScenario(scenario)
}

// RunCustomScenario runs a custom scenario configuration
func (sr *ScenarioRunner) RunCustomScenario(scenario *Scenario) error {
	sr.mu.Lock()
	sr.currentScenario = scenario
	sr.metrics = &ScenarioMetrics{StartTime: time.Now()}
	sr.mu.Unlock()

	sr.log.WithFields(logrus.Fields{
		"scenario":          scenario.Name,
		"delay_ms":          scenario.DelayMS,
		"failure_rate":      scenario.FailureRate,
		"initial_queue":     scenario.InitialQueue,
		"duration_secs":     scenario.DurationSecs,
		"task_submit_rate":  scenario.TaskSubmitRate,
	}).Info("Starting scenario")

	// Pre-fill queue if configured
	if scenario.InitialQueue > 0 {
		sr.log.Infof("Pre-filling queue with %d tasks", scenario.InitialQueue)
		for i := 0; i < scenario.InitialQueue; i++ {
			sr.service.AddPendingTask(fmt.Sprintf("prefill-%d", i))
		}
	}

	// Start task submission loop
	submissionCtx := make(chan struct{})
	go sr.submitTasksLoop(scenario, submissionCtx)

	// Monitor scenario for configured duration
	time.Sleep(time.Duration(scenario.DurationSecs) * time.Second)

	close(submissionCtx)
	sr.log.Info("Scenario completed")

	sr.mu.Lock()
	sr.metrics.EndTime = time.Now()
	sr.mu.Unlock()

	sr.printMetrics()

	return nil
}

// submitTasksLoop continuously submits tasks at configured rate
func (sr *ScenarioRunner) submitTasksLoop(scenario *Scenario, stopChan chan struct{}) {
	ticker := time.NewTicker(time.Duration(1000/scenario.TaskSubmitRate) * time.Millisecond)
	defer ticker.Stop()

	taskCounter := scenario.InitialQueue

	for {
		select {
		case <-stopChan:
			return
		case <-ticker.C:
			taskCounter++
			taskID := fmt.Sprintf("scenario-%s-task-%d", scenario.Name, taskCounter)
			tenantID := "scenario-tenant"

			req := map[string]interface{}{
				"task_id":         taskID,
				"tenant_id":       tenantID,
				"image":           "busybox:latest",
				"cpu_request":     "100m",
				"memory_request":  "128Mi",
				"priority":        10,
				"timeout_seconds": 300,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if _, err := sr.service.SubmitTask(ctx, req); err != nil {
				sr.log.Warnf("Failed to submit task %s: %v", taskID, err)
			}
			cancel()

			sr.metrics.mu.Lock()
			sr.metrics.TotalSubmitted++
			sr.metrics.mu.Unlock()

			// Update peak queue depth
			queueResp, _ := sr.service.GetQueueDepth(context.Background())
			if queueResp != nil {
				if qd, ok := queueResp["total_queued"].(int32); ok {
					sr.metrics.mu.Lock()
					if qd > sr.metrics.PeakQueueDepth {
						sr.metrics.PeakQueueDepth = qd
					}
					sr.metrics.mu.Unlock()
				}
			}
		}
	}
}

// findScenario finds a scenario by name
func (sr *ScenarioRunner) findScenario(name string) *Scenario {
	scenarios := PredefinedScenarios()
	for i := range scenarios {
		if scenarios[i].Name == name {
			return &scenarios[i]
		}
	}
	return nil
}

// printMetrics prints scenario execution metrics
func (sr *ScenarioRunner) printMetrics() {
	sr.metrics.mu.RLock()
	defer sr.metrics.mu.RUnlock()

	duration := sr.metrics.EndTime.Sub(sr.metrics.StartTime).Seconds()

	sr.log.WithFields(logrus.Fields{
		"total_submitted":   sr.metrics.TotalSubmitted,
		"total_completed":   sr.metrics.TotalCompleted,
		"total_failed":      sr.metrics.TotalFailed,
		"success_rate":      fmt.Sprintf("%.2f%%", float64(sr.metrics.TotalCompleted)*100/float64(sr.metrics.TotalSubmitted+1)),
		"peak_queue_depth":  sr.metrics.PeakQueueDepth,
		"duration_seconds":  duration,
		"throughput":        fmt.Sprintf("%.2f tasks/sec", float64(sr.metrics.TotalSubmitted)/duration),
	}).Info("Scenario metrics")
}

// GetMetrics returns current scenario metrics
func (sr *ScenarioRunner) GetMetrics() *ScenarioMetrics {
	sr.metrics.mu.RLock()
	defer sr.metrics.mu.RUnlock()

	metricsCopy := &ScenarioMetrics{
		TotalSubmitted:  sr.metrics.TotalSubmitted,
		TotalCompleted:  sr.metrics.TotalCompleted,
		TotalFailed:     sr.metrics.TotalFailed,
		AverageDuration: sr.metrics.AverageDuration,
		PeakQueueDepth:  sr.metrics.PeakQueueDepth,
		StartTime:       sr.metrics.StartTime,
		EndTime:         sr.metrics.EndTime,
	}

	return metricsCopy
}

// Stop stops the current scenario
func (sr *ScenarioRunner) Stop() {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	sr.log.Info("Stopping current scenario")
	close(sr.stopChan)
}
