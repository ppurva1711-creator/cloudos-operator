import http from 'k6/http';
import { check, group, sleep } from 'k6';
import { Counter, Trend, Gauge } from 'k6/metrics';

// ============================================================================
// Configuration
// ============================================================================

const BASE_URL = __ENV.BASE_URL || 'https://cloudtask.local';
const JWT_TOKEN = __ENV.JWT_TOKEN || 'default-jwt-token';
const TENANT_ID = __ENV.TENANT_ID || 'tenant-a';

// ============================================================================
// Custom Metrics
// ============================================================================

const taskCreatedCounter = new Counter('task_created_stress');
const taskFailureCounter = new Counter('task_failures_stress');
const scalingEventsCounter = new Counter('scaling_events_detected');
const errorCounter = new Counter('http_errors_stress');
const requestDuration = new Trend('request_duration_ms_stress');
const activeConnections = new Gauge('active_connections_stress');
const errorRate = new Gauge('error_rate_stress');

// ============================================================================
// Load Test Configuration - Stress Test with Spike
// ============================================================================

export const options = {
  stages: [
    // Ramp up: 0 to 200 users over 2 minutes
    { duration: '2m', target: 200, tags: { stage: 'ramp-up' } },
    // Hold: 200 users for 5 minutes - look for stability
    { duration: '5m', target: 200, tags: { stage: 'hold' } },
    // Spike: jump to 500 users for 1 minute - watch for errors/scaling
    { duration: '1m', target: 500, tags: { stage: 'spike' } },
    // Recovery: drop to 50 users for 2 minutes - observe scaling down
    { duration: '2m', target: 50, tags: { stage: 'recovery' } },
    // Final ramp down
    { duration: '1m', target: 0, tags: { stage: 'ramp-down' } },
  ],
  thresholds: {
    http_req_duration: ['p(99)<1000'],  // p99 latency < 1s during stress
    http_req_failed: ['rate<0.05'],     // tolerate up to 5% errors during stress
  },
};

// ============================================================================
// Utility Functions
// ============================================================================

function generateJWT() {
  return JWT_TOKEN;
}

function generateTaskName() {
  return `stress-task-${Date.now()}-${Math.floor(Math.random() * 100000)}`;
}

function generateTaskPayload() {
  // Vary the task configurations to create diverse workloads
  const taskTypes = [
    { image: 'alpine:latest', command: ['sh', '-c'], args: ['sleep 5'] },
    { image: 'alpine:latest', command: ['sh', '-c'], args: ['echo "test" && sleep 3'] },
    { image: 'busybox:latest', command: ['sh', '-c'], args: ['dd if=/dev/zero bs=1M count=100'] },
  ];

  const taskType = taskTypes[Math.floor(Math.random() * taskTypes.length)];

  return {
    name: generateTaskName(),
    image: taskType.image,
    tenantID: TENANT_ID,
    command: taskType.command,
    args: taskType.args,
    retries: 0,
    timeout: '15m',
    priority: Math.floor(Math.random() * 100),
    resources: {
      requests: {
        cpu: '200m',
        memory: '256Mi',
      },
      limits: {
        cpu: '1000m',
        memory: '1Gi',
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

  const startTime = new Date().getTime();
  const res = http.post(`${BASE_URL}/v1/tasks`, JSON.stringify(payload), params);
  const duration = new Date().getTime() - startTime;

  requestDuration.add(duration);

  if (res.status === 429) {
    // Rate limited
    check(res, {
      'rate limited': () => true,
    });
    return null;
  }

  check(res, {
    'create task status is 201': (r) => r.status === 201,
  }) || errorCounter.add(1);

  if (res.status === 201) {
    taskCreatedCounter.add(1);
    try {
      const body = JSON.parse(res.body);
      return body.name || payload.name;
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
      return JSON.parse(res.body);
    } catch {
      return null;
    }
  }

  return null;
}

// ============================================================================
// Main Test Scenario
// ============================================================================

export default function () {
  group('Stress Test', function () {
    // Attempt to create a task
    const taskName = createTask();

    if (!taskName) {
      return;
    }

    // Minimal polling - just check status 2-3 times then move on
    for (let i = 0; i < 3; i++) {
      const taskStatus = getTaskStatus(taskName);
      if (!taskStatus) {
        taskFailureCounter.add(1);
        break;
      }

      if (taskStatus.phase === 'Completed' || taskStatus.phase === 'Failed') {
        break;
      }

      sleep(1);
    }
  });

  sleep(0.1); // Very minimal delay to increase concurrency
}

// ============================================================================
// Thresholds and Monitoring
// ============================================================================

export function handleSummary(data) {
  return {
    'stdout': textSummary(data, { indent: ' ', enableColors: true }),
  };
}

function textSummary(data, options) {
  const indent = options.indent || '';
  let summary = '';

  const metrics = data.metrics;
  if (metrics) {
    if (metrics.http_req_failed && metrics.http_req_failed.values) {
      const failureRate = Math.round(metrics.http_req_failed.values.rate * 100 * 100) / 100;
      summary += `\n${indent}Failed Requests: ${failureRate}%`;
    }

    if (metrics.task_created_stress && metrics.task_created_stress.values) {
      summary += `\n${indent}Tasks Created: ${metrics.task_created_stress.values.count}`;
    }

    if (metrics.http_errors_stress && metrics.http_errors_stress.values) {
      summary += `\n${indent}Errors: ${metrics.http_errors_stress.values.count}`;
    }
  }

  return summary;
}
