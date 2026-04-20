package storage

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "github.com/lib/pq"
	"github.com/sirupsen/logrus"
)

// TaskHistoryStore defines the interface for storing and retrieving task execution history
type TaskHistoryStore interface {
	// RecordTaskStart records when a task starts execution
	RecordTaskStart(ctx context.Context, taskID, tenantID, podName, namespace string) error

	// RecordTaskCompletion records a successful task completion
	RecordTaskCompletion(ctx context.Context, taskID, podName string, exitCode int32, duration time.Duration) error

	// RecordTaskFailure records a task failure
	RecordTaskFailure(ctx context.Context, taskID, podName, reason, message string, exitCode int32, duration time.Duration, retryAttempt, maxRetries int) error

	// GetTaskHistory retrieves task history for a tenant
	GetTaskHistory(ctx context.Context, filter TaskFilter) ([]TaskRecord, error)

	// GetTaskByID retrieves a specific task record by ID
	GetTaskByID(ctx context.Context, taskID, tenantID string) (*TaskRecord, error)

	// GetTenantStats retrieves aggregated statistics for a tenant
	GetTenantStats(ctx context.Context, tenantID string, days int) (*TaskExecutionStats, error)

	// GetRecentFailures retrieves recent failures for a tenant
	GetRecentFailures(ctx context.Context, tenantID string, limit int) ([]RecentFailure, error)

	// Close closes the database connection
	Close() error
}

// PostgresStore implements TaskHistoryStore using PostgreSQL
type PostgresStore struct {
	db     *sql.DB
	log    *logrus.Logger
	mu     sync.RWMutex
	closed bool
}

// NewPostgresStore creates a new PostgreSQL-backed task history store
func NewPostgresStore(connString string, log *logrus.Logger) (*PostgresStore, error) {
	// Open connection with retry logic
	var db *sql.DB
	var err error
	maxRetries := 5
	retryDelay := time.Second

	for i := 0; i < maxRetries; i++ {
		db, err = sql.Open("postgres", connString)
		if err != nil {
			log.Warnf("Failed to open database connection (attempt %d/%d): %v", i+1, maxRetries, err)
			if i < maxRetries-1 {
				time.Sleep(retryDelay)
				retryDelay *= 2
			}
			continue
		}

		// Test connection
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = db.PingContext(ctx)
		cancel()

		if err != nil {
			log.Warnf("Failed to ping database (attempt %d/%d): %v", i+1, maxRetries, err)
			db.Close()
			if i < maxRetries-1 {
				time.Sleep(retryDelay)
				retryDelay *= 2
			}
			continue
		}

		break
	}

	if err != nil {
		return nil, fmt.Errorf("failed to connect to PostgreSQL after %d attempts: %w", maxRetries, err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)
	db.SetConnMaxIdleTime(10 * time.Minute)

	log.Infof("Connected to PostgreSQL and configured connection pool (max 10, idle 5)")

	return &PostgresStore{
		db:  db,
		log: log,
	}, nil
}

// RecordTaskStart records when a task starts execution
func (ps *PostgresStore) RecordTaskStart(ctx context.Context, taskID, tenantID, podName, namespace string) error {
	ps.mu.RLock()
	if ps.closed {
		ps.mu.RUnlock()
		return fmt.Errorf("store is closed")
	}
	ps.mu.RUnlock()

	query := `
		INSERT INTO task_executions (task_id, tenant_id, pod_name, namespace, status, started_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id
	`

	var id string
	err := ps.db.QueryRowContext(
		ctx, query,
		taskID, tenantID, podName, namespace, TaskStatusRunning, time.Now(),
	).Scan(&id)

	if err != nil {
		ps.log.Errorf("Failed to record task start for task %s: %v", taskID, err)
		return fmt.Errorf("failed to record task start: %w", err)
	}

	ps.log.Debugf("Recorded task start for task %s (pod %s)", taskID, podName)
	return nil
}

// RecordTaskCompletion records a successful task completion
func (ps *PostgresStore) RecordTaskCompletion(ctx context.Context, taskID, podName string, exitCode int32, duration time.Duration) error {
	ps.mu.RLock()
	if ps.closed {
		ps.mu.RUnlock()
		return fmt.Errorf("store is closed")
	}
	ps.mu.RUnlock()

	query := `
		UPDATE task_executions
		SET
			status = $1,
			exit_code = $2,
			duration_seconds = $3,
			completed_at = $4
		WHERE pod_name = $5 AND task_id = $6 AND status != $7
	`

	result, err := ps.db.ExecContext(
		ctx, query,
		TaskStatusCompleted, exitCode, duration.Seconds(), time.Now(),
		podName, taskID, TaskStatusCompleted,
	)

	if err != nil {
		ps.log.Errorf("Failed to record task completion for task %s: %v", taskID, err)
		return fmt.Errorf("failed to record task completion: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		ps.log.Errorf("Failed to get rows affected: %v", err)
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		ps.log.Warnf("No rows updated for task completion (task %s, pod %s)", taskID, podName)
		return fmt.Errorf("task not found or already completed")
	}

	ps.log.Debugf("Recorded task completion for task %s (pod %s) with exit code %d in %v", taskID, podName, exitCode, duration)
	return nil
}

// RecordTaskFailure records a task failure
func (ps *PostgresStore) RecordTaskFailure(ctx context.Context, taskID, podName, reason, message string, exitCode int32, duration time.Duration, retryAttempt, maxRetries int) error {
	ps.mu.RLock()
	if ps.closed {
		ps.mu.RUnlock()
		return fmt.Errorf("store is closed")
	}
	ps.mu.RUnlock()

	query := `
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
	`

	result, err := ps.db.ExecContext(
		ctx, query,
		TaskStatusFailed, exitCode, duration.Seconds(), time.Now(),
		reason, message, retryAttempt, maxRetries,
		podName, taskID, TaskStatusFailed,
	)

	if err != nil {
		ps.log.Errorf("Failed to record task failure for task %s: %v", taskID, err)
		return fmt.Errorf("failed to record task failure: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		ps.log.Errorf("Failed to get rows affected: %v", err)
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		ps.log.Warnf("No rows updated for task failure (task %s, pod %s)", taskID, podName)
		return fmt.Errorf("task not found or already failed")
	}

	ps.log.Debugf("Recorded task failure for task %s (pod %s): %s - %s", taskID, podName, reason, message)
	return nil
}

// GetTaskHistory retrieves task history for a tenant
func (ps *PostgresStore) GetTaskHistory(ctx context.Context, filter TaskFilter) ([]TaskRecord, error) {
	ps.mu.RLock()
	if ps.closed {
		ps.mu.RUnlock()
		return nil, fmt.Errorf("store is closed")
	}
	ps.mu.RUnlock()

	if filter.TenantID == "" {
		return nil, fmt.Errorf("tenant_id is required")
	}

	if filter.Limit == 0 {
		filter.Limit = 100
	}

	query := `
		SELECT
			id, task_id, tenant_id, pod_name, namespace, container_id,
			status, exit_code, retry_attempt, max_retries,
			started_at, completed_at, duration_seconds,
			reason, message,
			created_at, updated_at
		FROM task_executions
		WHERE tenant_id = $1
	`
	args := []interface{}{filter.TenantID}
	argIdx := 2

	if filter.Status != "" {
		query += fmt.Sprintf(" AND status = $%d", argIdx)
		args = append(args, string(filter.Status))
		argIdx++
	}

	if filter.OnlyFailed {
		query += fmt.Sprintf(" AND status = $%d", argIdx)
		args = append(args, string(TaskStatusFailed))
		argIdx++
	}

	if !filter.StartDate.IsZero() {
		query += fmt.Sprintf(" AND created_at >= $%d", argIdx)
		args = append(args, filter.StartDate)
		argIdx++
	}

	if !filter.EndDate.IsZero() {
		query += fmt.Sprintf(" AND created_at <= $%d", argIdx)
		args = append(args, filter.EndDate)
		argIdx++
	}

	if filter.PodName != "" {
		query += fmt.Sprintf(" AND pod_name ILIKE $%d", argIdx)
		args = append(args, "%"+filter.PodName+"%")
		argIdx++
	}

	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", argIdx)
	args = append(args, filter.Limit)

	if filter.Offset > 0 {
		argIdx++
		query += fmt.Sprintf(" OFFSET $%d", argIdx)
		args = append(args, filter.Offset)
	}

	rows, err := ps.db.QueryContext(ctx, query, args...)
	if err != nil {
		ps.log.Errorf("Failed to query task history: %v", err)
		return nil, fmt.Errorf("failed to query task history: %w", err)
	}
	defer rows.Close()

	var records []TaskRecord
	for rows.Next() {
		var rec TaskRecord
		err := rows.Scan(
			&rec.ID, &rec.TaskID, &rec.TenantID, &rec.PodName, &rec.Namespace, &rec.ContainerID,
			&rec.Status, &rec.ExitCode, &rec.RetryAttempt, &rec.MaxRetries,
			&rec.StartedAt, &rec.CompletedAt, &rec.Duration,
			&rec.Reason, &rec.Message,
			&rec.CreatedAt, &rec.UpdatedAt,
		)
		if err != nil {
			ps.log.Errorf("Failed to scan task record: %v", err)
			return nil, fmt.Errorf("failed to scan task record: %w", err)
		}
		records = append(records, rec)
	}

	if err = rows.Err(); err != nil {
		ps.log.Errorf("Error iterating rows: %v", err)
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	ps.log.Debugf("Retrieved %d task records for tenant %s", len(records), filter.TenantID)
	return records, nil
}

// GetTaskByID retrieves a specific task record by ID
func (ps *PostgresStore) GetTaskByID(ctx context.Context, taskID, tenantID string) (*TaskRecord, error) {
	ps.mu.RLock()
	if ps.closed {
		ps.mu.RUnlock()
		return nil, fmt.Errorf("store is closed")
	}
	ps.mu.RUnlock()

	query := `
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
	`

	var rec TaskRecord
	err := ps.db.QueryRowContext(ctx, query, taskID, tenantID).Scan(
		&rec.ID, &rec.TaskID, &rec.TenantID, &rec.PodName, &rec.Namespace, &rec.ContainerID,
		&rec.Status, &rec.ExitCode, &rec.RetryAttempt, &rec.MaxRetries,
		&rec.StartedAt, &rec.CompletedAt, &rec.Duration,
		&rec.Reason, &rec.Message,
		&rec.CreatedAt, &rec.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			ps.log.Warnf("Task not found: %s for tenant %s", taskID, tenantID)
			return nil, fmt.Errorf("task not found")
		}
		ps.log.Errorf("Failed to query task by ID: %v", err)
		return nil, fmt.Errorf("failed to query task: %w", err)
	}

	ps.log.Debugf("Retrieved task record for task %s", taskID)
	return &rec, nil
}

// GetTenantStats retrieves aggregated statistics for a tenant
func (ps *PostgresStore) GetTenantStats(ctx context.Context, tenantID string, days int) (*TaskExecutionStats, error) {
	ps.mu.RLock()
	if ps.closed {
		ps.mu.RUnlock()
		return nil, fmt.Errorf("store is closed")
	}
	ps.mu.RUnlock()

	if days == 0 {
		days = 30
	}

	query := `
		SELECT
			total_tasks,
			completed_tasks,
			failed_tasks,
			success_rate,
			avg_duration_seconds,
			max_duration_seconds
		FROM get_tenant_stats($1, $2)
	`

	var stats TaskExecutionStats
	err := ps.db.QueryRowContext(ctx, query, tenantID, days).Scan(
		&stats.TotalTasks,
		&stats.CompletedTasks,
		&stats.FailedTasks,
		&stats.SuccessRate,
		&stats.AvgDuration,
		&stats.MaxDuration,
	)

	if err != nil {
		ps.log.Errorf("Failed to query tenant stats: %v", err)
		return nil, fmt.Errorf("failed to query tenant stats: %w", err)
	}

	ps.log.Debugf("Retrieved stats for tenant %s: total=%d, completed=%d, failed=%d, success_rate=%.2f%%",
		tenantID, stats.TotalTasks, stats.CompletedTasks, stats.FailedTasks, stats.SuccessRate)
	return &stats, nil
}

// GetRecentFailures retrieves recent failures for a tenant
func (ps *PostgresStore) GetRecentFailures(ctx context.Context, tenantID string, limit int) ([]RecentFailure, error) {
	ps.mu.RLock()
	if ps.closed {
		ps.mu.RUnlock()
		return nil, fmt.Errorf("store is closed")
	}
	ps.mu.RUnlock()

	if limit == 0 {
		limit = 10
	}

	query := `
		SELECT
			id, task_id, pod_name, reason, message, completed_at, created_at
		FROM get_recent_failures($1, $2)
	`

	rows, err := ps.db.QueryContext(ctx, query, tenantID, limit)
	if err != nil {
		ps.log.Errorf("Failed to query recent failures: %v", err)
		return nil, fmt.Errorf("failed to query recent failures: %w", err)
	}
	defer rows.Close()

	var failures []RecentFailure
	for rows.Next() {
		var failure RecentFailure
		var failedAt sql.NullTime
		err := rows.Scan(
			&failure.ID,
			&failure.TaskID,
			&failure.PodName,
			&failure.Reason,
			&failure.Message,
			&failedAt,
			&failure.CreatedAt,
		)
		if err != nil {
			ps.log.Errorf("Failed to scan failure record: %v", err)
			return nil, fmt.Errorf("failed to scan failure record: %w", err)
		}
		if failedAt.Valid {
			failure.FailedAt = failedAt.Time
		}
		failures = append(failures, failure)
	}

	if err = rows.Err(); err != nil {
		ps.log.Errorf("Error iterating failure rows: %v", err)
		return nil, fmt.Errorf("error iterating failure rows: %w", err)
	}

	ps.log.Debugf("Retrieved %d recent failures for tenant %s", len(failures), tenantID)
	return failures, nil
}

// Close closes the database connection
func (ps *PostgresStore) Close() error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if ps.closed {
		return nil
	}

	if err := ps.db.Close(); err != nil {
		ps.log.Errorf("Failed to close database connection: %v", err)
		return fmt.Errorf("failed to close database: %w", err)
	}

	ps.closed = true
	ps.log.Infof("PostgreSQL connection closed")
	return nil
}
