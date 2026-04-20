package storage

import (
	"context"
	"database/sql"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRecordTaskStart tests recording a task start
func TestRecordTaskStart(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	store := &PostgresStore{db: db, log: logger, closed: false}

	// Setup mock expectations
	mock.ExpectQuery(regexp.QuoteMeta(`
		INSERT INTO task_executions (task_id, tenant_id, pod_name, namespace, status, started_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id
	`)).
		WithArgs("task-123", "tenant-1", "pod-abc", "default", TaskStatusRunning, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("uuid-123"))

	// Test
	ctx := context.Background()
	err = store.RecordTaskStart(ctx, "task-123", "tenant-1", "pod-abc", "default")

	// Verify
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestRecordTaskCompletion tests recording a task completion
func TestRecordTaskCompletion(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	logger := logrus.New()
	store := &PostgresStore{db: db, log: logger, closed: false}

	// Setup mock expectations
	mock.ExpectExec(regexp.QuoteMeta(`
		UPDATE task_executions
		SET
			status = $1,
			exit_code = $2,
			duration_seconds = $3,
			completed_at = $4
		WHERE pod_name = $5 AND task_id = $6 AND status != $7
	`)).
		WithArgs(TaskStatusCompleted, int32(0), float64(5), sqlmock.AnyArg(), "pod-abc", "task-123", TaskStatusCompleted).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Test
	ctx := context.Background()
	err = store.RecordTaskCompletion(ctx, "task-123", "pod-abc", 0, 5*time.Second)

	// Verify
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestRecordTaskFailure tests recording a task failure
func TestRecordTaskFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	logger := logrus.New()
	store := &PostgresStore{db: db, log: logger, closed: false}

	// Setup mock expectations
	mock.ExpectExec(regexp.QuoteMeta(`
		UPDATE task_executions
		SET
			status = $1,
			exit_code = $2,
			duration_seconds = $3,
			completed_at = $4,
			reason = $5,
			message = $6,
			retry_attempt = $7,
			max_retries = $8
		WHERE pod_name = $9 AND task_id = $10 AND status != $11
	`)).
		WithArgs(TaskStatusFailed, int32(1), float64(3), sqlmock.AnyArg(), "CrashLoopBackOff", "Container exited", 1, 3, "pod-abc", "task-123", TaskStatusFailed).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Test
	ctx := context.Background()
	err = store.RecordTaskFailure(ctx, "task-123", "pod-abc", "CrashLoopBackOff", "Container exited", 1, 3*time.Second, 1, 3)

	// Verify
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestGetTaskHistory tests retrieving task history
func TestGetTaskHistory(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	logger := logrus.New()
	store := &PostgresStore{db: db, log: logger, closed: false}

	// Setup mock expectations
	rows := sqlmock.NewRows([]string{
		"id", "task_id", "tenant_id", "pod_name", "namespace", "container_id",
		"status", "exit_code", "retry_attempt", "max_retries",
		"started_at", "completed_at", "duration_seconds",
		"reason", "message",
		"created_at", "updated_at",
	}).
		AddRow(
			"uuid-123", "task-123", "tenant-1", "pod-abc", "default", nil,
			TaskStatusCompleted, 0, 0, 0,
			time.Now(), time.Now(), 5000,
			nil, nil,
			time.Now(), time.Now(),
		)

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			id, task_id, tenant_id, pod_name, namespace, container_id,
			status, exit_code, retry_attempt, max_retries,
			started_at, completed_at, duration_seconds,
			reason, message,
			created_at, updated_at
		FROM task_executions
		WHERE tenant_id = $1
	 ORDER BY created_at DESC LIMIT $2`)).
		WithArgs("tenant-1", 100).
		WillReturnRows(rows)

	// Test
	ctx := context.Background()
	filter := TaskFilter{
		TenantID: "tenant-1",
		Limit:    100,
	}
	records, err := store.GetTaskHistory(ctx, filter)

	// Verify
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, "task-123", records[0].TaskID)
	assert.Equal(t, TaskStatusCompleted, records[0].Status)
	assert.Equal(t, int32(0), records[0].ExitCode.Int32)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestGetTaskByID tests retrieving a task by ID
func TestGetTaskByID(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	logger := logrus.New()
	store := &PostgresStore{db: db, log: logger, closed: false}

	// Setup mock expectations
	rows := sqlmock.NewRows([]string{
		"id", "task_id", "tenant_id", "pod_name", "namespace", "container_id",
		"status", "exit_code", "retry_attempt", "max_retries",
		"started_at", "completed_at", "duration_seconds",
		"reason", "message",
		"created_at", "updated_at",
	}).
		AddRow(
			"uuid-123", "task-123", "tenant-1", "pod-abc", "default", nil,
			TaskStatusCompleted, 0, 0, 0,
			time.Now(), time.Now(), 5000,
			nil, nil,
			time.Now(), time.Now(),
		)

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			id, task_id, tenant_id, pod_name, namespace, container_id,
			status, exit_code, retry_attempt, max_retries,
			started_at, completed_at, duration_seconds,
			reason, message,
			created_at, updated_at
		FROM task_executions
		WHERE task_id = $1 AND tenant_id = $2
		ORDER BY created_at DESC
		LIMIT 1
	`)).
		WithArgs("task-123", "tenant-1").
		WillReturnRows(rows)

	// Test
	ctx := context.Background()
	record, err := store.GetTaskByID(ctx, "task-123", "tenant-1")

	// Verify
	require.NoError(t, err)
	require.NotNil(t, record)
	assert.Equal(t, "task-123", record.TaskID)
	assert.Equal(t, TaskStatusCompleted, record.Status)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestGetTenantStats tests retrieving tenant statistics
func TestGetTenantStats(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	logger := logrus.New()
	store := &PostgresStore{db: db, log: logger, closed: false}

	// Setup mock expectations
	rows := sqlmock.NewRows([]string{
		"total_tasks", "completed_tasks", "failed_tasks", "success_rate", "avg_duration_seconds", "max_duration_seconds",
	}).
		AddRow(100, 95, 5, 95.0, 2500.5, 15000)

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			total_tasks,
			completed_tasks,
			failed_tasks,
			success_rate,
			avg_duration_seconds,
			max_duration_seconds
		FROM get_tenant_stats($1, $2)
	`)).
		WithArgs("tenant-1", 30).
		WillReturnRows(rows)

	// Test
	ctx := context.Background()
	stats, err := store.GetTenantStats(ctx, "tenant-1", 30)

	// Verify
	require.NoError(t, err)
	require.NotNil(t, stats)
	assert.Equal(t, int64(100), stats.TotalTasks)
	assert.Equal(t, int64(95), stats.CompletedTasks)
	assert.Equal(t, int64(5), stats.FailedTasks)
	assert.Equal(t, 95.0, stats.SuccessRate)
	assert.Equal(t, 2500.5, stats.AvgDuration)
	assert.Equal(t, int64(15000), stats.MaxDuration)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestGetRecentFailures tests retrieving recent failures
func TestGetRecentFailures(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	logger := logrus.New()
	store := &PostgresStore{db: db, log: logger, closed: false}

	// Setup mock expectations
	rows := sqlmock.NewRows([]string{
		"id", "task_id", "pod_name", "reason", "message", "completed_at", "created_at",
	}).
		AddRow("uuid-456", "task-456", "pod-def", "CrashLoopBackOff", "Container exited", time.Now(), time.Now()).
		AddRow("uuid-789", "task-789", "pod-ghi", "ImagePullBackOff", "Failed to pull image", time.Now(), time.Now())

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			id, task_id, pod_name, reason, message, completed_at, created_at
		FROM get_recent_failures($1, $2)
	`)).
		WithArgs("tenant-1", 10).
		WillReturnRows(rows)

	// Test
	ctx := context.Background()
	failures, err := store.GetRecentFailures(ctx, "tenant-1", 10)

	// Verify
	require.NoError(t, err)
	require.Len(t, failures, 2)
	assert.Equal(t, "task-456", failures[0].TaskID)
	assert.Equal(t, "CrashLoopBackOff", failures[0].Reason)
	assert.Equal(t, "task-789", failures[1].TaskID)
	assert.Equal(t, "ImagePullBackOff", failures[1].Reason)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestRecordTaskCompletion_NotFound tests recording completion for non-existent task
func TestRecordTaskCompletion_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	logger := logrus.New()
	store := &PostgresStore{db: db, log: logger, closed: false}

	// Setup mock - return 0 rows affected
	mock.ExpectExec(regexp.QuoteMeta(`
		UPDATE task_executions
		SET
			status = $1,
			exit_code = $2,
			duration_seconds = $3,
			completed_at = $4
		WHERE pod_name = $5 AND task_id = $6 AND status != $7
	`)).
		WithArgs(TaskStatusCompleted, int32(0), float64(5), sqlmock.AnyArg(), "pod-xyz", "task-xyz", TaskStatusCompleted).
		WillReturnResult(sqlmock.NewResult(0, 0))

	// Test
	ctx := context.Background()
	err = store.RecordTaskCompletion(ctx, "task-xyz", "pod-xyz", 0, 5*time.Second)

	// Verify - should return error for 0 rows affected
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found or already completed")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestGetTaskByID_NotFound tests retrieving non-existent task
func TestGetTaskByID_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	logger := logrus.New()
	store := &PostgresStore{db: db, log: logger, closed: false}

	// Setup mock - no rows
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			id, task_id, tenant_id, pod_name, namespace, container_id,
			status, exit_code, retry_attempt, max_retries,
			started_at, completed_at, duration_seconds,
			reason, message,
			created_at, updated_at
		FROM task_executions
		WHERE task_id = $1 AND tenant_id = $2
		ORDER BY created_at DESC
		LIMIT 1
	`)).
		WithArgs("task-xyz", "tenant-xyz").
		WillReturnError(sql.ErrNoRows)

	// Test
	ctx := context.Background()
	record, err := store.GetTaskByID(ctx, "task-xyz", "tenant-xyz")

	// Verify
	require.Error(t, err)
	assert.Nil(t, record)
	assert.Contains(t, err.Error(), "task not found")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestClose tests closing the store
func TestClose(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	logger := logrus.New()
	store := &PostgresStore{db: db, log: logger, closed: false}

	// Setup mock expectation for Close
	mock.ExpectClose()

	// Test
	err = store.Close()

	// Verify
	require.NoError(t, err)
	assert.True(t, store.closed)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestClose_AlreadyClosed tests closing an already closed store
func TestClose_AlreadyClosed(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	logger := logrus.New()
	store := &PostgresStore{db: db, log: logger, closed: true}

	// Test
	err = store.Close()

	// Verify
	require.NoError(t, err)
	assert.True(t, store.closed)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestRecordTaskStart_StoreClosed tests recording start with closed store
func TestRecordTaskStart_StoreClosed(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	logger := logrus.New()
	store := &PostgresStore{db: db, log: logger, closed: true}

	// Test
	ctx := context.Background()
	err = store.RecordTaskStart(ctx, "task-123", "tenant-1", "pod-abc", "default")

	// Verify
	require.Error(t, err)
	assert.Contains(t, err.Error(), "store is closed")
}

// TestGetTaskHistory_FilteredByStatus tests task history with status filter
func TestGetTaskHistory_FilteredByStatus(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	logger := logrus.New()
	store := &PostgresStore{db: db, log: logger, closed: false}

	// Setup mock expectations with status filter
	rows := sqlmock.NewRows([]string{
		"id", "task_id", "tenant_id", "pod_name", "namespace", "container_id",
		"status", "exit_code", "retry_attempt", "max_retries",
		"started_at", "completed_at", "duration_seconds",
		"reason", "message",
		"created_at", "updated_at",
	})

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			id, task_id, tenant_id, pod_name, namespace, container_id,
			status, exit_code, retry_attempt, max_retries,
			started_at, completed_at, duration_seconds,
			reason, message,
			created_at, updated_at
		FROM task_executions
		WHERE tenant_id = $1
 AND status = $2 ORDER BY created_at DESC LIMIT $3`)).
		WithArgs("tenant-1", TaskStatusFailed, 100).
		WillReturnRows(rows)

	// Test
	ctx := context.Background()
	filter := TaskFilter{
		TenantID: "tenant-1",
		Status:   TaskStatusFailed,
		Limit:    100,
	}
	records, err := store.GetTaskHistory(ctx, filter)

	// Verify
	require.NoError(t, err)
	require.Len(t, records, 0) // Empty results
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestGetTaskHistory_RequiresTenantID tests that tenant_id is required
func TestGetTaskHistory_RequiresTenantID(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	logger := logrus.New()
	store := &PostgresStore{db: db, log: logger, closed: false}

	// Test
	ctx := context.Background()
	filter := TaskFilter{
		// TenantID is empty
		Limit: 100,
	}
	records, err := store.GetTaskHistory(ctx, filter)

	// Verify
	require.Error(t, err)
	assert.Nil(t, records)
	assert.Contains(t, err.Error(), "tenant_id is required")
}
