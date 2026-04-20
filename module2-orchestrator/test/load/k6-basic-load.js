import http from 'k6/http';
import { check, group, sleep } from 'k6';
import { Counter, Trend, Gauge } from 'k6/metrics';

// ============================================================================
// Configuration
// ============================================================================

const BASE_URL = __ENV.BASE_URL || 'https://cloudtask.local';
const JWT_TOKEN = __ENV.JWT_TOKEN || 'default-jwt-token';
const TENANT_ID = __ENV.TENANT_ID || 'tenant-a';
const TASK_POLL_INTERVAL = 2; // seconds
const TASK_POLL_MAX_RETRIES = 150; // 5 minutes

// ============================================================================
// Custom Metrics
// ============================================================================

const taskCreatedCounter = new Counter('task_created');
const taskCompletedCounter = new Counter('task_completed');
const taskFailedCounter = new Counter('task_failed');
const taskDeletedCounter = new Counter('task_deleted');
const taskCompletionTime = new Trend('task_completion_time_ms');
const pollRetries = new Trend('poll_retries');
const errorCounter = new Counter('http_errors');

// ============================================================================
// Load Test Configuration
// ============================================================================

export const options = {
  stages: [
    // Ramp up: 0 to 50 users over 1 minute
    { duration: '1m', target: 50 },
    // Steady state: 50 users for 3 minutes
    { duration: '3m', target: 50 },
    // Ramp down: 50 to 0 users over 1 minute
    { duration: '1m', target: 0 },
  ],
  thresholds: {
    http_req_duration: ['p(95)<500'],           // p95 latency < 500ms
    http_req_failed: ['rate<0.01'],             // error rate < 1%
    task_completion_time_ms: ['p(95)<30000'],  // task completes p95 < 30s
  },
};

// ============================================================================
// Utility Functions
// ============================================================================

function generateJWT() {
  // In production, generate fresh JWT tokens
  // For now, use provided token from environment
  return JWT_TOKEN;
}

function generateTaskName() {
  return `task-${Date.now()}-${Math.floor(Math.random() * 10000)}`;
}

function generateTaskPayload() {
  const commands = [
    ['echo', 'hello'],
    ['sleep', '5'],
    ['true'],
  ];
  const cmdIdx = Math.floor(Math.random() * commands.length);
  const [cmd, ...args] = commands[cmdIdx];

  return {
    name: generateTaskName(),
    image: 'alpine:latest',
    tenantID: TENANT_ID,
    command: [cmd],
    args: args,
    retries: 1,
    timeout: '10m',
    priority: Math.floor(Math.random() * 100),
    resources: {
      requests: {
        cpu: '100m',
        memory: '128Mi',
      },
      limits: {
        cpu: '500m',
        memory: '512Mi',
      },
    },
  };
}

function createHeaders() {
  return {
    'Authorization': `Bearer ${generateJWT()}`,
    'Content-Type': 'application/json',
  };
}

// ============================================================================
// Test Functions
// ============================================================================

function createTask() {
  const payload = generateTaskPayload();
  const params = {
    headers: createHeaders(),
    timeout: '30s',
    tags: { name: 'CreateTask' },
  };

  const res = http.post(`${BASE_URL}/v1/tasks`, JSON.stringify(payload), params);

  check(res, {
    'create task status is 201': (r) => r.status === 201,
    'create task has task name': (r) => {
      try {
        const body = JSON.parse(r.body);
        return body.name && body.name !== '';
      } catch {
        return false;
      }
    },
  }) || errorCounter.add(1);

  if (res.status === 201) {
    taskCreatedCounter.add(1);
    try {
      const body = JSON.parse(res.body);
      return body.name;
    } catch {
      return payload.name;
    }
  }

  return null;
}

function getTaskStatus(taskName) {
  const params = {
    headers: createHeaders(),
    timeout: '10s',
    tags: { name: 'GetTask' },
  };

  const res = http.get(`${BASE_URL}/v1/tasks/${taskName}`, params);

  if (res.status === 200) {
    try {
      const body = JSON.parse(res.body);
      return {
        status: body.phase,
        name: body.name,
      };
    } catch {
      return null;
    }
  }

  return null;
}

function pollTaskCompletion(taskName, maxRetries = TASK_POLL_MAX_RETRIES) {
  let attempts = 0;
  const startTime = new Date().getTime();

  while (attempts < maxRetries) {
    const taskStatus = getTaskStatus(taskName);

    if (!taskStatus) {
      break;
    }

    const status = taskStatus.status;

    // Terminal states
    if (status === 'Completed' || status === 'Failed' || status === 'Cancelled') {
      const completionTime = new Date().getTime() - startTime;
      taskCompletionTime.add(completionTime);
      pollRetries.add(attempts);

      if (status === 'Completed') {
        taskCompletedCounter.add(1);
      } else {
        taskFailedCounter.add(1);
      }

      check(true, {
        'task completed': () => status === 'Completed',
      });

      return true;
    }

    // Still running, wait and retry
    sleep(TASK_POLL_INTERVAL);
    attempts++;
  }

  pollRetries.add(attempts);
  check(false, {
    'task did not complete within timeout': () => false,
  });

  return false;
}

function deleteTask(taskName) {
  const params = {
    headers: createHeaders(),
    timeout: '10s',
    tags: { name: 'DeleteTask' },
  };

  const res = http.del(`${BASE_URL}/v1/tasks/${taskName}`, null, params);

  check(res, {
    'delete task status is 200 or 204': (r) => r.status === 200 || r.status === 204,
  }) || errorCounter.add(1);

  if (res.status === 200 || res.status === 204) {
    taskDeletedCounter.add(1);
  }
}

// ============================================================================
// Main Test Scenario
// ============================================================================

export default function () {
  group('Task Lifecycle', function () {
    // Create a task
    const taskName = createTask();

    if (!taskName) {
      return;
    }

    // Poll for completion
    pollTaskCompletion(taskName);

    // Clean up
    deleteTask(taskName);
  });

  // Small delay between iterations
  sleep(1);
}

// ============================================================================
// Setup and Teardown
// ============================================================================

export function setup() {
  // Health check
  const res = http.get(`${BASE_URL}/health`);
  check(res, {
    'health check passed': (r) => r.status === 200,
  });
}

export function teardown() {
  // Optional: cleanup resources
}
