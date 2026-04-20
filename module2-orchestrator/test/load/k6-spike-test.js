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

const tasksCreatedInSpike = new Counter('tasks_created_in_spike');
const tasksDropped = new Counter('tasks_dropped_spike');
const requestsSuccessful = new Counter('requests_successful_spike');
const requestsFailed = new Counter('requests_failed_spike');
const spikeResponseTime = new Trend('spike_response_time_ms');
const timeToScale = new Gauge('time_to_scale_up_ms');
const podsScaledUp = new Gauge('pods_scaled_up_count');
const recoveryTime = new Gauge('recovery_time_ms');

// ============================================================================
// Load Test Configuration - Sudden Traffic Spike
// ============================================================================

export const options = {
  stages: [
    // Baseline: 5 users for 1 minute
    { duration: '1m', target: 5, tags: { stage: 'baseline' } },
    // Instant spike: jump to 300 users
    { duration: '2m', target: 300, tags: { stage: 'spike' } },
    // Recovery: drop back to 5 users
    { duration: '1m', target: 5, tags: { stage: 'recovery' } },
  ],
  thresholds: {
    http_req_duration: ['p(95)<1000'],
    http_req_failed: ['rate<0.10'], // Allow higher failure rate during spike
  },
};

// ============================================================================
// Utility Functions
// ============================================================================

function generateJWT() {
  return JWT_TOKEN;
}

function generateTaskName() {
  return `spike-task-${Date.now()}-${Math.floor(Math.random() * 100000)}`;
}

function generateTaskPayload() {
  const taskNames = [
    { command: ['sh', '-c'], args: ['echo "spike test"'] },
    { command: ['sh', '-c'], args: ['sleep 2'] },
    { command: ['sh', '-c'], args: ['for i in $(seq 1 10); do echo $i; done'] },
  ];

  const task = taskNames[Math.floor(Math.random() * taskNames.length)];

  return {
    name: generateTaskName(),
    image: 'alpine:latest',
    tenantID: TENANT_ID,
    command: task.command,
    args: task.args,
    retries: 0,
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

  const startTime = new Date().getTime();
  const res = http.post(`${BASE_URL}/v1/tasks`, JSON.stringify(payload), params);
  const duration = new Date().getTime() - startTime;

  spikeResponseTime.add(duration);

  if (res.status === 201) {
    requestsSuccessful.add(1);
    tasksCreatedInSpike.add(1);
    try {
      const body = JSON.parse(res.body);
      return body.name || payload.name;
    } catch {
      return payload.name;
    }
  } else if (res.status >= 500 || res.status === 429) {
    // 5xx errors or rate limiting
    tasksDropped.add(1);
    requestsFailed.add(1);
    return null;
  } else {
    requestsFailed.add(1);
    return null;
  }
}

function checkTaskStatus(taskName) {
  const params = {
    headers: createHeaders(),
    timeout: '10s',
    tags: { name: 'GetTask' },
  };

  const startTime = new Date().getTime();
  const res = http.get(`${BASE_URL}/v1/tasks/${taskName}`, params);
  const duration = new Date().getTime() - startTime;

  spikeResponseTime.add(duration);

  return res.status === 200;
}

// ============================================================================
// Main Test Scenario
// ============================================================================

export default function () {
  group('Spike Test', function () {
    // Attempt to create task
    const taskName = createTask();

    if (taskName) {
      // Quick status check
      sleep(0.5);
      checkTaskStatus(taskName);
    }
  });

  sleep(0.2);
}

// ============================================================================
// Monitoring and Reporting
// ============================================================================

export function handleSummary(data) {
  const summary = {
    'stdout': generateReport(data),
  };
  return summary;
}

function generateReport(data) {
  const metrics = data.metrics;
  let report = '\n=== SPIKE TEST SUMMARY ===\n';

  if (metrics.tasks_created_in_spike) {
    report += `\nTasks Created: ${metrics.tasks_created_in_spike.values.count}`;
  }

  if (metrics.tasks_dropped_spike) {
    report += `\nTasks Dropped: ${metrics.tasks_dropped_spike.values.count}`;
  }

  if (metrics.requests_successful_spike) {
    report += `\nSuccessful Requests: ${metrics.requests_successful_spike.values.count}`;
  }

  if (metrics.requests_failed_spike) {
    report += `\nFailed Requests: ${metrics.requests_failed_spike.values.count}`;
  }

  if (metrics.spike_response_time_ms) {
    const times = metrics.spike_response_time_ms.values;
    report += `\nResponse Times (ms):`;
    report += `\n  Avg: ${Math.round(times.avg || 0)}`;
    report += `\n  P50: ${Math.round(times['p(50)'] || 0)}`;
    report += `\n  P95: ${Math.round(times['p(95)'] || 0)}`;
    report += `\n  P99: ${Math.round(times['p(99)'] || 0)}`;
  }

  if (metrics.http_req_failed) {
    const failRate = (metrics.http_req_failed.values.rate * 100).toFixed(2);
    report += `\nOverall Failure Rate: ${failRate}%`;
  }

  report += '\n======================\n';
  return report;
}
