-- Migration: 001_create_task_executions
-- Description: Create task_executions table for storing CloudTask execution history
-- Created: 2026-04-19

-- ============================================================================
-- Create ENUM types
-- ============================================================================

-- Task status enumeration
CREATE TYPE task_status AS ENUM (
    'pending',
    'running',
    'completed',
    'failed',
    'cancelled',
    'paused'
);

-- ============================================================================
-- Create task_executions table
-- ============================================================================

CREATE TABLE IF NOT EXISTS task_executions (
    -- Primary identifiers
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id VARCHAR(255) NOT NULL,
    tenant_id VARCHAR(255) NOT NULL,
    
    -- Pod information
    pod_name VARCHAR(255) NOT NULL,
    namespace VARCHAR(255) DEFAULT 'default' NOT NULL,
    container_id VARCHAR(255),
    
    -- Task status
    status task_status DEFAULT 'pending' NOT NULL,
    exit_code INTEGER,
    retry_attempt INTEGER DEFAULT 0,
    max_retries INTEGER DEFAULT 0,
    
    -- Timing
    started_at TIMESTAMP WITH TIME ZONE,
    completed_at TIMESTAMP WITH TIME ZONE,
    duration_seconds BIGINT,
    
    -- Metadata
    reason VARCHAR(100),
    message TEXT,
    
    -- Timestamps
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP NOT NULL,
    
    -- Constraints
    CONSTRAINT task_duration_positive CHECK (duration_seconds >= 0),
    CONSTRAINT exit_code_valid CHECK (exit_code >= -1 AND exit_code <= 255),
    CONSTRAINT retry_attempt_valid CHECK (retry_attempt >= 0),
    CONSTRAINT completed_after_started CHECK (completed_at IS NULL OR started_at IS NULL OR completed_at >= started_at)
);

-- ============================================================================
-- Create indexes for efficient querying
-- ============================================================================

-- Index for tenant-specific queries with task_id
CREATE INDEX idx_tenant_task ON task_executions (tenant_id, task_id)
    WHERE created_at > CURRENT_TIMESTAMP - INTERVAL '90 days';

-- Index for status-based filtering
CREATE INDEX idx_status ON task_executions (status, created_at DESC)
    WHERE created_at > CURRENT_TIMESTAMP - INTERVAL '30 days';

-- Index for time-based queries
CREATE INDEX idx_created_at ON task_executions (created_at DESC)
    WHERE status IN ('completed', 'failed');

-- Index for tenant-based history retrieval
CREATE INDEX idx_tenant_created ON task_executions (tenant_id, created_at DESC)
    WHERE created_at > CURRENT_TIMESTAMP - INTERVAL '90 days';

-- Index for task lifecycle queries
CREATE INDEX idx_task_lifecycle ON task_executions (task_id, tenant_id, created_at DESC);

-- ============================================================================
-- Create partitioning support views (optional, for large datasets)
-- ============================================================================

CREATE VIEW task_executions_monthly AS
SELECT
    DATE_TRUNC('month', created_at) AS month,
    COUNT(*) as execution_count,
    COUNT(CASE WHEN status = 'completed' THEN 1 END) as completed_count,
    COUNT(CASE WHEN status = 'failed' THEN 1 END) as failed_count,
    ROUND(AVG(COALESCE(duration_seconds, 0))::numeric, 2) as avg_duration_seconds,
    MAX(duration_seconds) as max_duration_seconds
FROM task_executions
GROUP BY DATE_TRUNC('month', created_at)
ORDER BY month DESC;

-- ============================================================================
-- Create helper functions
-- ============================================================================

-- Function to get task execution stats by tenant
CREATE OR REPLACE FUNCTION get_tenant_stats(p_tenant_id VARCHAR(255), p_days INTEGER DEFAULT 30)
RETURNS TABLE (
    total_tasks BIGINT,
    completed_tasks BIGINT,
    failed_tasks BIGINT,
    success_rate NUMERIC,
    avg_duration_seconds NUMERIC,
    max_duration_seconds BIGINT
) AS $$
BEGIN
    RETURN QUERY
    SELECT
        COUNT(*)::BIGINT,
        COUNT(CASE WHEN status = 'completed' THEN 1 END)::BIGINT,
        COUNT(CASE WHEN status = 'failed' THEN 1 END)::BIGINT,
        ROUND(
            COUNT(CASE WHEN status = 'completed' THEN 1 END)::NUMERIC / NULLIF(COUNT(*)::NUMERIC, 0) * 100,
            2
        ) as success_rate,
        ROUND(AVG(COALESCE(duration_seconds, 0))::NUMERIC, 2),
        MAX(duration_seconds)::BIGINT
    FROM task_executions
    WHERE tenant_id = p_tenant_id
    AND created_at > CURRENT_TIMESTAMP - (p_days || ' days')::INTERVAL;
END;
$$ LANGUAGE plpgsql STABLE;

-- Function to get recent failures for a tenant
CREATE OR REPLACE FUNCTION get_recent_failures(p_tenant_id VARCHAR(255), p_limit INTEGER DEFAULT 10)
RETURNS TABLE (
    id UUID,
    task_id VARCHAR(255),
    pod_name VARCHAR(255),
    reason VARCHAR(100),
    message TEXT,
    failed_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE
) AS $$
BEGIN
    RETURN QUERY
    SELECT
        task_executions.id,
        task_executions.task_id,
        task_executions.pod_name,
        task_executions.reason,
        task_executions.message,
        task_executions.completed_at,
        task_executions.created_at
    FROM task_executions
    WHERE tenant_id = p_tenant_id
    AND status = 'failed'
    AND created_at > CURRENT_TIMESTAMP - INTERVAL '30 days'
    ORDER BY created_at DESC
    LIMIT p_limit;
END;
$$ LANGUAGE plpgsql STABLE;

-- ============================================================================
-- Create audit trigger for updated_at
-- ============================================================================

CREATE OR REPLACE FUNCTION update_task_executions_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_task_executions_updated_at
    BEFORE UPDATE ON task_executions
    FOR EACH ROW
    EXECUTE FUNCTION update_task_executions_updated_at();

-- ============================================================================
-- Grant permissions (adjust as needed)
-- ============================================================================

-- Grant select to read-only users for monitoring
GRANT SELECT ON task_executions TO PUBLIC;
GRANT SELECT ON task_executions_monthly TO PUBLIC;

-- ============================================================================
-- Add sample data (optional, for testing)
-- ============================================================================

-- Uncomment to add sample data:
-- INSERT INTO task_executions (
--     task_id, tenant_id, pod_name, status, exit_code, 
--     started_at, completed_at, duration_seconds, created_at
-- ) VALUES (
--     'task-001', 'tenant-1', 'task-001-pod', 'completed', 0,
--     CURRENT_TIMESTAMP - INTERVAL '1 hour',
--     CURRENT_TIMESTAMP - INTERVAL '55 minutes',
--     300,
--     CURRENT_TIMESTAMP - INTERVAL '1 hour'
-- );
