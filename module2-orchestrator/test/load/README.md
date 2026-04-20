# Load Testing Suite for CloudTask Orchestrator

This directory contains comprehensive load testing and performance analysis tools for the CloudTask Orchestrator system.

## Files Overview

### k6 Test Scripts

1. **k6-basic-load.js** - Basic load test for normal operations
   - Ramp up: 0→50 users (1 min)
   - Steady state: 50 users (3 min)
   - Ramp down: 50→0 users (1 min)
   - Verifies: API stability, task lifecycle, basic thresholds

2. **k6-stress-test.js** - Stress test to find breaking points
   - Ramp up: 0→200 users (2 min)
   - Hold: 200 users (5 min)
   - Spike: →500 users (1 min)
   - Recovery: →50 users (2 min)
   - Verifies: Scaling triggers, error handling, recovery

3. **k6-tenant-isolation-load.js** - Multi-tenant isolation under load
   - 5 tenants × 20 concurrent users each
   - Verifies: No cross-tenant data leakage, fair resource sharing, rate limiting

4. **k6-spike-test.js** - Sudden traffic spike simulation
   - Baseline: 5 users (1 min)
   - Spike: →300 users (2 min)
   - Recovery: →5 users (1 min)
   - Verifies: KEDA responsiveness, drop rate, recovery time

### Results Directory

`results/` - Test execution results (created after running tests)
- JSON output from k6 (one file per test run)
- Analysis reports (bottleneck-analysis-*.txt)

## Quick Usage

```bash
# Set required environment variables
export JWT_TOKEN="your-jwt-token"
export JWT_TOKEN_TENANT_A="token-a"
export JWT_TOKEN_TENANT_B="token-b"
export JWT_TOKEN_TENANT_C="token-c"
export JWT_TOKEN_TENANT_D="token-d"
export JWT_TOKEN_TENANT_E="token-e"

# Run tests
cd ../..  # Go to project root
./scripts/run-load-tests.sh basic
./scripts/run-load-tests.sh stress
./scripts/run-load-tests.sh tenant
./scripts/run-load-tests.sh spike

# Analyze results
./scripts/analyze-bottlenecks.sh

# Fix bottlenecks (requires dry-run review first)
./scripts/fix-bottlenecks.sh --dry-run
./scripts/fix-bottlenecks.sh
```

## Test Thresholds

### Basic Load Test
- P95 HTTP latency: < 500ms
- Error rate: < 1%
- Task completion (P95): < 30s

### Stress Test
- P99 HTTP latency: < 1s (relaxed during stress)
- Error rate: < 5% (tolerant during spike)

### Tenant Isolation
- Cross-tenant access blocking: 100% (all attempts blocked)
- Fair resource distribution: ±10% per tenant

### Spike Test
- Tasks dropped: < 5%
- Recovery time: < 2 minutes

## Metrics Collected

Each test collects:
- HTTP request metrics (latency, success, failure)
- Task lifecycle metrics (create, complete, fail)
- Custom business metrics (queue depth, resource allocation)
- Resource utilization (CPU, memory)

## Integration

Tests are designed to work with:
- **k6** - Load testing framework
- **Prometheus** - Metrics collection
- **KEDA** - Auto-scaling system
- **Kubernetes** - Container orchestration
- **PostgreSQL** - Task persistence
- **Redis** - Event pub/sub

## Troubleshooting

### Tests fail with "JWT token not provided"
```bash
export JWT_TOKEN="eyJ0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiJ9..."
```

### k6 not found
```bash
# Install k6
brew install k6          # macOS
sudo apt-get install k6  # Linux
choco install k6         # Windows
```

### API Gateway not responding
```bash
# Check service status
kubectl get svc -n orchestrator-system api-gateway

# Port forward if needed
kubectl port-forward -n orchestrator-system svc/api-gateway 8080:8080
```

## See Also

- [LOAD-TESTING-GUIDE.md](../LOAD-TESTING-GUIDE.md) - Comprehensive guide
- [run-load-tests.sh](../../scripts/run-load-tests.sh) - Orchestration script
- [analyze-bottlenecks.sh](../../scripts/analyze-bottlenecks.sh) - Analysis tool
- [fix-bottlenecks.sh](../../scripts/fix-bottlenecks.sh) - Optimization script
