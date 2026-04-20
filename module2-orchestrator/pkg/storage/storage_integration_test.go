package storage

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestStore creates a PostgresStore backed by sqlmock
func newTestStore(t *testing.T) (*PostgresStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	store := &PostgresStore{
		db:  db,
		log: log,
	}

	t.Cleanup(func() { db.Close() })
	return store, mock
}

// ---- Task Lifecycle: RecordTaskStart → RecordTaskCompletion ----

func TestStorageIntegration_TaskLifecycleStartToCompletion(t *testing.T) {
	store, mock := newTestStore(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	taskID := "task-001"
	tenantID := "tenant-a"
	podName := "task-001-pod"
	namespace := "tenant-a"

	// RecordTaskStart uses QueryRowContext with RETURNING id
	mock.ExpectQuery("INSERT INTO task_executions").
		WithArgs(
			taskID,
			tenantID,
			podName,
			namespace,
			TaskStatusRunning,
			sqlmock.AnyArg(), // time.Now()
		).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("record-001"))

	err := store.RecordTaskStart(ctx, taskID, tenantID, podName, namespace)
	require.NoError(t, err, "RecordTaskStart should succeed")

	// RecordTaskCompletion uses ExecContext
	duration := 30 * time.Second
	mock.ExpectExec("UPDATE task_executions").
		WithArgs(
			TaskStatusCompleted,
			int32(0),
			duration.Seconds(),
			sqlmock.AnyArg(), // time.Now()
			podName,
			taskID,
			TaskStatusCompleted,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.RecordTaskCompletion(ctx, taskID, podName, 0, duration)
	require.NoError(t, err, "RecordTaskCompletion should succeed")

	assert.NoError(t, mock.ExpectationsWereMet(), "All mock expectations should be met")
}

// ---- Task Lifecycle: RecordTaskStart → RecordTaskFailure ----

func TestStorageIntegration_TaskLifecycleStartToFailure(t *testing.T) {
	store, mock := newTestStore(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	taskID := "task-002"
	tenantID := "tenant-b"
	podName := "task-002-pod"
	namespace := "tenant-b"

	// RecordTaskStart
	mock.ExpectQuery("INSERT INTO task_executions").
		WithArgs(
			taskID,
			tenantID,
			podName,
			namespace,
			TaskStatusRunning,
			sqlmock.AnyArg(),
		).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("record-002"))

	err := store.RecordTaskStart(ctx, taskID, tenantID, podName, namespace)
	require.NoError(t, err)

	// RecordTaskFailure uses ExecContext
	duration := 5 * time.Second
	mock.ExpectExec("UPDATE task_executions").
		WithArgs(
			TaskStatusFailed,
			int32(1),
			duration.Seconds(),
			sqlmock.AnyArg(), // completed_at
			"CrashLoopBackOff",
			"Container failed with exit code 1",
			0,    // retryAttempt
			3,    // maxRetries
			podName,
			taskID,
			TaskStatusFailed,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.RecordTaskFailure(ctx, taskID, podName, "CrashLoopBackOff", "Container failed with exit code 1", 1, duration, 0, 3)
	require.NoError(t, err, "RecordTaskFailure should succeed")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---- Task Not Found After Completion ----

func TestStorageIntegration_TaskCompletionNotFound(t *testing.T) {
	store, mock := newTestStore(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Update affects 0 rows → "task not found or already completed"
	mock.ExpectExec("UPDATE task_executions").
		WithArgs(
			TaskStatusCompleted,
			int32(0),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			"missing-pod",
			"missing-task",
			TaskStatusCompleted,
		).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := store.RecordTaskCompletion(ctx, "missing-task", "missing-pod", 0, time.Second)
	assert.Error(t, err, "Should error when no rows updated")
	assert.Contains(t, err.Error(), "not found")
}

// ---- GetTaskHistory Pagination ----

func TestStorageIntegration_GetTaskHistoryPagination(t *testing.T) {
	store, mock := newTestStore(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tenantID := "tenant-a"
	now := time.Now()

	// 17 columns matching postgres.go scan
	columns := []string{
		"id", "task_id", "tenant_id", "pod_name", "namespace", "container_id",
		"status", "exit_code", "retry_attempt", "max_retries",
		"started_at", "completed_at", "duration_seconds",
		"reason", "message",
		"created_at", "updated_at",
	}

	// Page 1: 10 records
	rows := sqlmock.NewRows(columns)
	for i := 0; i < 10; i++ {
		rows.AddRow(
			fmt.Sprintf("record-%d", i),
			fmt.Sprintf("task-%03d", i),
			tenantID,
			fmt.Sprintf("pod-%d", i),
			"tenant-a",
			nil, // container_id
			"completed",
			int32(0),
			0,
			0,
			now.Add(-time.Duration(i)*time.Hour),
			now.Add(-time.Duration(i)*time.Hour).Add(30*time.Second),
			int64(30),
			nil, // reason
			nil, // message
			now,
			now,
		)
	}

	mock.ExpectQuery("SELECT .+ FROM task_executions").
		WillReturnRows(rows)

	filter := TaskFilter{
		TenantID: tenantID,
		Limit:    10,
		Offset:   0,
	}

	records, err := store.GetTaskHistory(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, 10, len(records), "Page 1 should return 10 records")
	assert.Equal(t, "task-000", records[0].TaskID)

	// Page 2: 5 records (last page)
	rows2 := sqlmock.NewRows(columns)
	for i := 10; i < 15; i++ {
		rows2.AddRow(
			fmt.Sprintf("record-%d", i),
			fmt.Sprintf("task-%03d", i),
			tenantID,
			fmt.Sprintf("pod-%d", i),
			"tenant-a",
			nil,
			"completed",
			int32(0),
			0,
			0,
			now.Add(-time.Duration(i)*time.Hour),
			now.Add(-time.Duration(i)*time.Hour).Add(30*time.Second),
			int64(30),
			nil,
			nil,
			now,
			now,
		)
	}

	mock.ExpectQuery("SELECT .+ FROM task_executions").
		WillReturnRows(rows2)

	filter.Offset = 10
	records, err = store.GetTaskHistory(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, 5, len(records), "Page 2 should return 5 records")
	assert.Equal(t, "task-010", records[0].TaskID)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---- GetTaskHistory with Empty TenantID ----

func TestStorageIntegration_GetTaskHistoryMissingTenant(t *testing.T) {
	store, _ := newTestStore(t)

	ctx := context.Background()

	_, err := store.GetTaskHistory(ctx, TaskFilter{TenantID: ""})
	assert.Error(t, err, "Empty tenant_id should error")
	assert.Contains(t, err.Error(), "tenant_id is required")
}

// ---- GetTaskByID ----

func TestStorageIntegration_GetTaskByID(t *testing.T) {
	store, mock := newTestStore(t)

	ctx := context.Background()
	now := time.Now()

	columns := []string{
		"id", "task_id", "tenant_id", "pod_name", "namespace", "container_id",
		"status", "exit_code", "retry_attempt", "max_retries",
		"started_at", "completed_at", "duration_seconds",
		"reason", "message",
		"created_at", "updated_at",
	}

	rows := sqlmock.NewRows(columns).AddRow(
		"record-001",
		"task-001",
		"tenant-a",
		"task-001-pod",
		"tenant-a",
		nil,
		"completed",
		int32(0),
		0,
		0,
		now,
		now.Add(30*time.Second),
		int64(30),
		nil,
		nil,
		now,
		now,
	)

	mock.ExpectQuery("SELECT .+ FROM task_executions WHERE task_id").
		WithArgs("task-001", "tenant-a").
		WillReturnRows(rows)

	record, err := store.GetTaskByID(ctx, "task-001", "tenant-a")
	require.NoError(t, err)
	assert.NotNil(t, record)
	assert.Equal(t, "task-001", record.TaskID)
	assert.Equal(t, "tenant-a", record.TenantID)
	assert.Equal(t, TaskStatusCompleted, record.Status)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---- GetTaskByID Not Found ----

func TestStorageIntegration_GetTaskByIDNotFound(t *testing.T) {
	store, mock := newTestStore(t)

	ctx := context.Background()

	columns := []string{
		"id", "task_id", "tenant_id", "pod_name", "namespace", "container_id",
		"status", "exit_code", "retry_attempt", "max_retries",
		"started_at", "completed_at", "duration_seconds",
		"reason", "message",
		"created_at", "updated_at",
	}

	mock.ExpectQuery("SELECT .+ FROM task_executions WHERE task_id").
		WithArgs("task-999", "tenant-a").
		WillReturnRows(sqlmock.NewRows(columns)) // empty result

	record, err := store.GetTaskByID(ctx, "task-999", "tenant-a")
	assert.Error(t, err, "Not found should error")
	assert.Nil(t, record)
	assert.Contains(t, err.Error(), "task not found")
}

// ---- GetTenantStats ----

func TestStorageIntegration_GetTenantStats(t *testing.T) {
	store, mock := newTestStore(t)

	ctx := context.Background()

	rows := sqlmock.NewRows([]string{
		"total_tasks", "completed_tasks", "failed_tasks",
		"success_rate", "avg_duration_seconds", "max_duration_seconds",
	}).AddRow(int64(100), int64(95), int64(5), 95.0, float64(45.5), int64(300))

	mock.ExpectQuery("SELECT .+ FROM get_tenant_stats").
		WithArgs("tenant-a", 7).
		WillReturnRows(rows)

	stats, err := store.GetTenantStats(ctx, "tenant-a", 7)
	require.NoError(t, err)
	assert.NotNil(t, stats)
	assert.Equal(t, int64(100), stats.TotalTasks)
	assert.Equal(t, int64(95), stats.CompletedTasks)
	assert.Equal(t, int64(5), stats.FailedTasks)
	assert.Equal(t, 95.0, stats.SuccessRate)
	assert.Equal(t, 45.5, stats.AvgDuration)
	assert.Equal(t, int64(300), stats.MaxDuration)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---- GetRecentFailures ----

func TestStorageIntegration_GetRecentFailures(t *testing.T) {
	store, mock := newTestStore(t)

	ctx := context.Background()
	now := time.Now()

	rows := sqlmock.NewRows([]string{
		"id", "task_id", "pod_name", "reason", "message", "completed_at", "created_at",
	})

	for i := 0; i < 5; i++ {
		rows.AddRow(
			fmt.Sprintf("failure-%d", i),
			fmt.Sprintf("task-%03d", i),
			fmt.Sprintf("pod-%d", i),
			"CrashLoopBackOff",
			"Container crashed",
			now.Add(-time.Duration(i)*time.Hour),
			now.Add(-time.Duration(i)*time.Hour),
		)
	}

	mock.ExpectQuery("SELECT .+ FROM get_recent_failures").
		WithArgs("tenant-a", 5).
		WillReturnRows(rows)

	failures, err := store.GetRecentFailures(ctx, "tenant-a", 5)
	require.NoError(t, err)
	assert.Equal(t, 5, len(failures))

	for i, failure := range failures {
		assert.Equal(t, fmt.Sprintf("task-%03d", i), failure.TaskID)
		assert.Equal(t, "CrashLoopBackOff", failure.Reason)
	}

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---- Concurrent Writes ----

func TestStorageIntegration_ConcurrentWrites(t *testing.T) {
	store, mock := newTestStore(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	numGoroutines := 10
	for i := 0; i < numGoroutines; i++ {
		mock.ExpectQuery("INSERT INTO task_executions").
			WithArgs(
				sqlmock.AnyArg(), // taskID
				"tenant-a",
				sqlmock.AnyArg(), // podName
				"tenant-a",
				TaskStatusRunning,
				sqlmock.AnyArg(), // started_at
			).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(fmt.Sprintf("record-%d", i)))
	}

	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			taskID := fmt.Sprintf("task-%03d", index)
			err := store.RecordTaskStart(ctx, taskID, "tenant-a", fmt.Sprintf("pod-%d", index), "tenant-a")
			errors <- err
		}(i)
	}

	wg.Wait()
	close(errors)

	successCount := 0
	for err := range errors {
		if err == nil {
			successCount++
		}
	}

	assert.Equal(t, numGoroutines, successCount, "All concurrent writes should succeed")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---- Store Close Behavior ----

func TestStorageIntegration_StoreClosed(t *testing.T) {
	store, mock := newTestStore(t)

	mock.ExpectClose()

	// Close the store
	err := store.Close()
	require.NoError(t, err)

	// Operations on closed store should fail
	ctx := context.Background()
	err = store.RecordTaskStart(ctx, "task", "tenant", "pod", "ns")
	assert.Error(t, err, "RecordTaskStart on closed store should fail")
	assert.Contains(t, err.Error(), "closed")

	err = store.RecordTaskCompletion(ctx, "task", "pod", 0, time.Second)
	assert.Error(t, err, "RecordTaskCompletion on closed store should fail")

	err = store.RecordTaskFailure(ctx, "task", "pod", "reason", "msg", 1, time.Second, 0, 3)
	assert.Error(t, err, "RecordTaskFailure on closed store should fail")

	_, err = store.GetTaskHistory(ctx, TaskFilter{TenantID: "t"})
	assert.Error(t, err, "GetTaskHistory on closed store should fail")

	_, err = store.GetTaskByID(ctx, "task", "tenant")
	assert.Error(t, err, "GetTaskByID on closed store should fail")

	_, err = store.GetTenantStats(ctx, "tenant", 7)
	assert.Error(t, err, "GetTenantStats on closed store should fail")

	_, err = store.GetRecentFailures(ctx, "tenant", 5)
	assert.Error(t, err, "GetRecentFailures on closed store should fail")

	// Double close should be fine
	err = store.Close()
	assert.NoError(t, err)
}

// ---- Filtered Query (status filter) ----

func TestStorageIntegration_FilteredQuery(t *testing.T) {
	store, mock := newTestStore(t)

	ctx := context.Background()
	now := time.Now()

	columns := []string{
		"id", "task_id", "tenant_id", "pod_name", "namespace", "container_id",
		"status", "exit_code", "retry_attempt", "max_retries",
		"started_at", "completed_at", "duration_seconds",
		"reason", "message",
		"created_at", "updated_at",
	}

	rows := sqlmock.NewRows(columns).AddRow(
		"record-001",
		"task-001",
		"tenant-a",
		"pod-001",
		"tenant-a",
		nil,
		"failed",
		int32(1),
		0,
		3,
		now,
		now,
		int64(5),
		"CrashLoopBackOff",
		"Container crashed",
		now,
		now,
	)

	mock.ExpectQuery("SELECT .+ FROM task_executions").
		WillReturnRows(rows)

	filter := TaskFilter{
		TenantID: "tenant-a",
		Status:   TaskStatusFailed,
		Limit:    10,
	}

	records, err := store.GetTaskHistory(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, 1, len(records))
	assert.Equal(t, TaskStatusFailed, records[0].Status)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---- Default Limit ----

func TestStorageIntegration_DefaultLimit(t *testing.T) {
	store, mock := newTestStore(t)

	ctx := context.Background()

	columns := []string{
		"id", "task_id", "tenant_id", "pod_name", "namespace", "container_id",
		"status", "exit_code", "retry_attempt", "max_retries",
		"started_at", "completed_at", "duration_seconds",
		"reason", "message",
		"created_at", "updated_at",
	}

	mock.ExpectQuery("SELECT .+ FROM task_executions").
		WillReturnRows(sqlmock.NewRows(columns))

	// Limit=0 should default to 100
	filter := TaskFilter{
		TenantID: "tenant-a",
		Limit:    0,
	}

	_, err := store.GetTaskHistory(ctx, filter)
	require.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---- Default Days for TenantStats ----

func TestStorageIntegration_TenantStatsDefaultDays(t *testing.T) {
	store, mock := newTestStore(t)

	ctx := context.Background()

	rows := sqlmock.NewRows([]string{
		"total_tasks", "completed_tasks", "failed_tasks",
		"success_rate", "avg_duration_seconds", "max_duration_seconds",
	}).AddRow(int64(0), int64(0), int64(0), 0.0, 0.0, int64(0))

	// days=0 defaults to 30
	mock.ExpectQuery("SELECT .+ FROM get_tenant_stats").
		WithArgs("tenant-a", 30).
		WillReturnRows(rows)

	_, err := store.GetTenantStats(ctx, "tenant-a", 0)
	require.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}
