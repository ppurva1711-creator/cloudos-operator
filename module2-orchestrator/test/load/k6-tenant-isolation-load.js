import http from 'k6/http';
import { check, group, sleep } from 'k6';
import { Counter, Trend, Gauge } from 'k6/metrics';

// ============================================================================
// Configuration
// ============================================================================

const BASE_URL = __ENV.BASE_URL || 'https://cloudtask.local';
const TENANT_TOKENS = {
  'tenant-a': __ENV.JWT_TOKEN_TENANT_A || 'default-token-a',
  'tenant-b': __ENV.JWT_TOKEN_TENANT_B || 'default-token-b',
  'tenant-c': __ENV.JWT_TOKEN_TENANT_C || 'default-token-c',
  'tenant-d': __ENV.JWT_TOKEN_TENANT_D || 'default-token-d',
  'tenant-e': __ENV.JWT_TOKEN_TENANT_E || 'default-token-e',
};

const TENANTS = Object.keys(TENANT_TOKENS);
const NUM_CONCURRENT_USERS_PER_TENANT = 20;

// ============================================================================
// Custom Metrics
// ============================================================================

const tasksCreatedPerTenant = new Counter('tasks_created_per_tenant');
const tasksCompletedPerTenant = new Counter('tasks_completed_per_tenant');
const crossTenantAccessAttempts = new Counter('cross_tenant_access_attempts');
const crossTenantAccessBlocked = new Counter('cross_tenant_access_blocked');
const rateLimitEvents = new Counter('rate_limit_events');
const latencyPerTenant = new Trend('latency_per_tenant_ms');
const tasksPerTenantGauge = new Gauge('tasks_per_tenant');
const fairnessScore = new Gauge('tenant_fairness_score');

// ============================================================================
// Load Test Configuration
// ============================================================================

export const options = {
  scenarios: {
    'tenant-isolation': {
      executor: 'per-vu-iterations',
      vus: NUM_CONCURRENT_USERS_PER_TENANT * TENANTS.length,
      iterations: 10,
      maxDuration: '10m',
    },
  },
  thresholds: {
    http_req_duration: ['p(95)<500'],
    http_req_failed: ['rate<0.01'],
    'cross_tenant_access_blocked': ['rate==1.0'], // All unauthorized cross-tenant requests should be blocked
  },
};

// ============================================================================
// Utility Functions
// ============================================================================

function getTenantForVU(vuID) {
  // Distribute VUs across tenants
  return TENANTS[vuID % TENANTS.length];
}

function generateTaskName(tenantID) {
  return `tenant-isolation-${tenantID}-${Date.now()}-${Math.floor(Math.random() * 10000)}`;
}

function generateTaskPayload(tenantID) {
  return {
    name: generateTaskName(tenantID),
    image: 'alpine:latest',
    tenantID: tenantID,
    command: ['echo'],
    args: [`Hello from ${tenantID}`],
    retries: 1,
    timeout: '5m',
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

function createHeaders(tenantID) {
  return {
    'Authorization': `Bearer ${TENANT_TOKENS[tenantID]}`,
    'Content-Type': 'application/json',
  };
}

// ============================================================================
// Test Functions
// ============================================================================

function createTaskForTenant(tenantID) {
  const payload = generateTaskPayload(tenantID);
  const params = {
    headers: createHeaders(tenantID),
    timeout: '30s',
    tags: { name: 'CreateTask', tenant: tenantID },
  };

  const startTime = new Date().getTime();
  const res = http.post(`${BASE_URL}/v1/tasks`, JSON.stringify(payload), params);
  const duration = new Date().getTime() - startTime;

  latencyPerTenant.add(duration, { tenant: tenantID });

  check(res, {
    'create task status is 201': (r) => r.status === 201,
  });

  if (res.status === 429) {
    rateLimitEvents.add(1, { tenant: tenantID });
  }

  if (res.status === 201) {
    tasksCreatedPerTenant.add(1, { tenant: tenantID });
    try {
      const body = JSON.parse(res.body);
      return body.name || payload.name;
    } catch {
      return payload.name;
    }
  }

  return null;
}

function getTaskStatus(tenantID, taskName) {
  const params = {
    headers: createHeaders(tenantID),
    timeout: '10s',
    tags: { name: 'GetTask', tenant: tenantID },
  };

  const res = http.get(`${BASE_URL}/v1/tasks/${taskName}`, params);

  if (res.status === 200) {
    tasksCompletedPerTenant.add(1, { tenant: tenantID });
    return JSON.parse(res.body);
  }

  return null;
}

function attemptXTenantAccess(sourceTenant, targetTenant, targetTaskName) {
  // Attempt to access task from different tenant
  const params = {
    headers: createHeaders(sourceTenant),
    timeout: '10s',
    tags: { name: 'XTenantAccess', from: sourceTenant, to: targetTenant },
  };

  crossTenantAccessAttempts.add(1, { from: sourceTenant, to: targetTenant });

  const res = http.get(`${BASE_URL}/v1/tasks/${targetTaskName}`, params);

  // Should be 403 Forbidden or 404 Not Found
  if (res.status === 403 || res.status === 404) {
    crossTenantAccessBlocked.add(1, { from: sourceTenant, to: targetTenant });
    check(res, {
      'cross-tenant access blocked': (r) => r.status === 403 || r.status === 404,
    });
  }
}

// ============================================================================
// Main Test Scenario
// ============================================================================

export default function () {
  const myTenant = getTenantForVU(__VU % TENANTS.length);

  group(`Tenant ${myTenant} Operations`, function () {
    // Create task in own tenant
    const taskName = createTaskForTenant(myTenant);

    if (!taskName) {
      return;
    }

    // Check task status
    getTaskStatus(myTenant, taskName);

    // Attempt to access from a different tenant (should fail)
    const otherTenant = TENANTS[(TENANTS.indexOf(myTenant) + 1) % TENANTS.length];
    if (otherTenant !== myTenant) {
      attemptXTenantAccess(otherTenant, myTenant, taskName);
    }
  });

  sleep(0.5);
}

// ============================================================================
// Setup
// ============================================================================

export function setup() {
  // Verify all tenants are accessible
  for (const tenant of TENANTS) {
    const res = http.get(`${BASE_URL}/health`);
    check(res, {
      'health check passed': (r) => r.status === 200,
    });
  }
}
