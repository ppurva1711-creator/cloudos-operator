# Module 2 Orchestrator - Complete Documentation

**Last Updated:** April 2026  
**Status:** Production Ready - Module 1 Integration Complete

---

## Table of Contents

1. [Quick Start](#quick-start)
2. [Architecture](#architecture)
3. [Installation & Setup](#installation--setup)
4. [API Reference](#api-reference)
5. [Module 1 Integration](#module-1-integration)
6. [RBAC & Multi-Tenancy](#rbac--multi-tenancy)
7. [Redis & Pub/Sub](#redis--pubsub)
8. [Deployment](#deployment)
9. [Troubleshooting](#troubleshooting)

---

## Quick Start

### 1. Prerequisites
- Docker Desktop installed and running
- kubectl installed
- Go 1.20+ (for development)
- 20GB free disk space
- 4GB+ RAM

### 2. Create Kind Cluster (5 minutes)
```bash
cd c:\Users\suraj\OneDrive\Desktop\scheduler\module2-orchestrator

# Create cluster
kind create cluster --name orchestrator --image kindest/node:v1.28.0 --wait 5m

# Create namespace
kubectl create namespace orchestrator-system

# Verify
kubectl get nodes
```

### 3. Deploy Infrastructure (10 minutes)
```bash
# Apply CRD
kubectl apply -f config/crd/cloudtask_crd.yaml

# Apply RBAC
kubectl apply -f config/rbac/rbac.yaml

# Deploy PostgreSQL
kubectl apply -f config/postgres/postgres-secret.yaml
kubectl apply -f config/postgres/postgres-service.yaml
kubectl apply -f config/postgres/postgres-pvc.yaml
kubectl apply -f config/postgres/postgres-deployment.yaml

# Deploy Redis
kubectl apply -f config/redis/redis-secret.yaml
kubectl apply -f config/redis/redis-service.yaml
kubectl apply -f config/redis/redis-pvc.yaml
kubectl apply -f config/redis/redis-deployment.yaml

# Deploy Module 1 Service
kubectl apply -f config/integration/module1-service.yaml
```

### 4. Build and Deploy Operator (5 minutes)
```bash
# Build operator image
docker build -f Dockerfile.operator -t cloudtask-operator:latest .

# Load to Kind
kind load docker-image cloudtask-operator:latest --name=orchestrator

# Deploy operator
kubectl apply -f config/operator/deployment.yaml

# Verify
kubectl get pods -n orchestrator-system
```

### 5. Test Integration
```bash
# Run E2E test
./scripts/integration-test.sh

# Expected: All 7 steps pass
# Total E2E latency: 4-10 seconds
```

---

## Architecture

### High-Level Overview

```
┌─────────────────────────────────────────────────────────┐
│                  Kubernetes Cluster                     │
├─────────────────────────────────────────────────────────┤
│                                                           │
│  ┌──────────────────┐    ┌──────────────────────┐       │
│  │  API Gateway     │    │  CloudTask Operator  │       │
│  │  (Port 8080)     │    │  (Reconciliation)    │       │
│  │                  │    │                      │       │
│  │ HandleCreateTask ├───►│ Create Pods/Monitor  │       │
│  │ Module1Client    │    │ Update Status        │       │
│  └──────────────────┘    └──────────────────────┘       │
│         │                          │                    │
│         ▼                          ▼                    │
│  ┌──────────────────────────────────────────────┐      │
│  │         Module 1 Scheduler (gRPC)            │      │
│  │ • SubmitTask (queue task)                    │      │
│  │ • GetTaskStatus (fetch status)               │      │
│  │ • CancelTask (cancel execution)              │      │
│  │ • GetQueueDepth (health check)               │      │
│  └──────────────────────────────────────────────┘      │
│                      │                                  │
├──────────────────────┼──────────────────────────────────┤
│                      │                                  │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐             │
│  │PostgreSQL│  │  Redis   │  │ Config   │             │
│  │ (Tasks)  │  │ (Pub/Sub)│  │ (RBAC)   │             │
│  └──────────┘  └──────────┘  └──────────┘             │
│                                                           │
└─────────────────────────────────────────────────────────┘
```

### Request Flow

```
User Submit Task
      │
      ▼
API Gateway: POST /api/v1/tasks
      │
      ├─► Validate fields
      │
      ├─► Call Module 1: SubmitTask()
      │   ├─ Check circuit breaker
      │   ├─ Retry with exponential backoff (max 3)
      │   └─ Return: task_id, queue_position (or 503 error)
      │
      ├─► Create CloudTask in Kubernetes
      │
      ├─► Operator watches CloudTask
      │   ├─ Create Pod with specified image
      │   ├─ Apply resource limits
      │   └─ Monitor execution
      │
      ├─► Pod executes task
      │
      ├─► Task completion event
      │   ├─ Module 1 publishes Redis event
      │   ├─ Operator subscribes to Redis
      │   └─ Update CloudTask status
      │
      └─► Return 202 Accepted to user
          queue_position: 3
          estimated_wait: 45s
```

### Key Components

#### API Gateway (`cmd/api-gateway/main.go`)
- HTTP endpoint for task submission
- Module 1 client integration
- Request validation and error handling
- 503 Service Unavailable when Module 1 is unreachable

#### CloudTask Operator (`controllers/cloudtask_controller.go`)
- Watches CloudTask resources
- Creates/manages Kubernetes Pods
- Monitors pod lifecycle
- Updates task status
- Subscribes to Redis events

#### Module 1 Real Client (`pkg/grpc/module1_client.go`)
- gRPC connection to Module 1 scheduler
- Circuit breaker pattern (prevents cascade failures)
- Exponential backoff retry logic
- Health checks every 30 seconds
- Thread-safe concurrent access

#### PostgreSQL
- Persistent storage for tasks
- Task history and metadata
- User audit trails

#### Redis
- Pub/Sub for task events (task:completed, task:failed)
- Cache for frequently accessed data
- Connection pooling

---

## Installation & Setup

### Prerequisites Verification
```bash
# Check Docker
docker --version
docker info

# Check Kubernetes
kubectl version --client

# Check Go (for development)
go version

# Check all required commands
kind version
```

### Step 1: Create Kind Cluster

```bash
# Create with custom config for ingress support
kind create cluster --name orchestrator --image kindest/node:v1.28.0 \
  --config config/kind/cluster.yaml --wait 5m

# Verify cluster
kubectl cluster-info
kubectl get nodes
```

**Cluster Configuration** (`config/kind/cluster.yaml`):
- Single control-plane node
- Insecure registry for local images
- Extra port mappings for services

### Step 2: Setup Namespaces and RBAC

```bash
# Create namespace
kubectl create namespace orchestrator-system

# Apply RBAC
kubectl apply -f config/rbac/rbac.yaml
kubectl apply -f config/rbac/namespace-template.yaml
kubectl apply -f config/rbac/namespace-tenant-a.yaml

# Verify
kubectl get ns
kubectl get serviceaccount -n orchestrator-system
```

**RBAC Setup**:
- Operator service account with necessary permissions
- Tenant-specific role bindings
- Resource quotas per tenant
- Network policies for isolation

### Step 3: Deploy PostgreSQL

```bash
# Create secrets
kubectl apply -f config/postgres/postgres-secret.yaml

# Create persistent volume and storage
kubectl apply -f config/postgres/postgres-pvc.yaml

# Deploy service
kubectl apply -f config/postgres/postgres-service.yaml

# Deploy database
kubectl apply -f config/postgres/postgres-deployment.yaml

# Wait for pod to be ready
kubectl wait --for=condition=ready pod -l app=postgres \
  -n orchestrator-system --timeout=300s

# Verify
kubectl get pods -n orchestrator-system -l app=postgres
```

**Database Initialization**:
- Schemas created automatically on first connection
- Task history tables
- User audit tables
- Indexes for query performance

### Step 4: Deploy Redis

```bash
# Create secrets
kubectl apply -f config/redis/redis-secret.yaml

# Create storage
kubectl apply -f config/redis/redis-pvc.yaml

# Deploy service
kubectl apply -f config/redis/redis-service.yaml

# Deploy Redis
kubectl apply -f config/redis/redis-deployment.yaml

# Wait for pod to be ready
kubectl wait --for=condition=ready pod -l app=redis \
  -n orchestrator-system --timeout=300s

# Test connection
kubectl exec -it pod/<redis-pod> -n orchestrator-system -- redis-cli ping
# Should return: PONG
```

**Redis Configuration**:
- Pub/Sub for task events
- Persistence enabled (RDB dumps)
- Default port: 6379
- Max memory: 512MB

### Step 5: Deploy Module 1 Service

```bash
# Deploy Kubernetes service
kubectl apply -f config/integration/module1-service.yaml

# Verify service created
kubectl get svc -n orchestrator-system module1-scheduler

# Test DNS resolution
kubectl run -it --rm debug --image=busybox --restart=Never -- \
  nslookup module1-scheduler.orchestrator-system
```

The service can be switched between:
- **Real mode**: ExternalName pointing to production Module 1
- **Mock mode**: ClusterIP with selector for mock deployment

### Step 6: Build and Deploy Operator

```bash
# Build operator image
docker build -f Dockerfile.operator \
  -t cloudtask-operator:latest .

# Load to Kind cluster
kind load docker-image cloudtask-operator:latest --name=orchestrator

# Apply operator deployment
kubectl apply -f config/operator/deployment.yaml

# Verify operator is running
kubectl get pods -n orchestrator-system -l app=operator
kubectl logs -f deployment/operator -n orchestrator-system

# Check for "CloudTask CRD initialized" message
```

### Step 7: Build and Deploy API Gateway

```bash
# Build API gateway image
docker build -f Dockerfile.gateway \
  -t cloudtask-api-gateway:latest .

# Load to Kind
kind load docker-image cloudtask-api-gateway:latest --name=orchestrator

# Deploy API gateway
kubectl apply -f config/operator/deployment.yaml  # includes gateway

# Verify
kubectl get pods -n orchestrator-system -l app=api-gateway
kubectl logs -f deployment/api-gateway -n orchestrator-system
```

---

## API Reference

### Submit Task

**Endpoint:** `POST /api/v1/tasks`

**Request:**
```json
{
  "name": "my-task",
  "tenant_id": "tenant-a",
  "image": "busybox:latest",
  "command": ["echo", "hello"],
  "args": ["world"],
  "environment": {
    "KEY": "value"
  },
  "resources": {
    "cpu": "100m",
    "memory": "64Mi"
  }
}
```

**Success Response (202 Accepted):**
```json
{
  "task_id": "task-abc123",
  "status": "submitted",
  "queue_position": 3,
  "estimated_wait_seconds": 45
}
```

**Error Response (503 Service Unavailable):**
```json
{
  "error": "Module 1 scheduler unavailable",
  "retry_after_seconds": 30
}
```

### Get Task Status

**Endpoint:** `GET /api/v1/tasks/{task_id}`

**Response:**
```json
{
  "task_id": "task-abc123",
  "status": "running",
  "pod_name": "cloudtask-abc123",
  "created_at": "2026-04-20T10:30:00Z",
  "started_at": "2026-04-20T10:30:05Z",
  "completed_at": null
}
```

### List Tasks

**Endpoint:** `GET /api/v1/tasks?tenant_id=tenant-a&status=running`

**Response:**
```json
{
  "tasks": [
    {
      "task_id": "task-abc123",
      "status": "running",
      "created_at": "2026-04-20T10:30:00Z"
    }
  ],
  "total": 1
}
```

### Cancel Task

**Endpoint:** `DELETE /api/v1/tasks/{task_id}`

**Response (204 No Content):**
```
(empty)
```

---

## Module 1 Integration

### Overview

The Real gRPC client replaces the mock scheduler with production-ready Module 1 integration.

**Key Features:**
- ✅ Circuit breaker pattern (prevents cascade failures)
- ✅ Exponential backoff retry (handles transient failures)
- ✅ Health checks (30-second interval)
- ✅ Thread-safe concurrent access
- ✅ Structured logging for all calls

### Configuration

**Environment Variables** (all optional - have defaults):

```bash
# Module 1 gRPC endpoint (default: module1-scheduler:50051)
export MODULE1_GRPC_ADDRESS="module1-scheduler:50051"

# gRPC call timeout (default: 30s)
export MODULE1_GRPC_TIMEOUT="30s"

# Maximum retry attempts (default: 3)
export MODULE1_GRPC_MAX_RETRIES="3"
```

### Resilience Patterns

#### Circuit Breaker
```
State Machine:
CLOSED (normal) 
  → 5 consecutive failures 
  → OPEN (reject all calls)
  → after 30s timeout
  → HALF-OPEN (allow probe call)
  → on success
  → CLOSED (reset)
```

**Behavior:**
- **CLOSED:** All calls pass through normally
- **OPEN:** All calls immediately return error (no network attempt)
- **HALF-OPEN:** Single test call allowed; success closes, failure opens again

#### Retry Logic
```
Exponential Backoff:
Attempt 1: Immediate
Attempt 2: Wait 1s, retry
Attempt 3: Wait 2s, retry
Attempt 4: Wait 4s, retry
All failed: Return error to client
```

### Implementation

**Module 1 Client Location:** `pkg/grpc/module1_client.go`

**Methods:**
```go
SubmitTask(ctx context.Context, req *SubmitTaskRequest) (*SubmitTaskResponse, error)
GetTaskStatus(ctx context.Context, taskID string) (*TaskStatusResponse, error)
CancelTask(ctx context.Context, taskID string) (*CancelTaskResponse, error)
GetQueueDepth(ctx context.Context) (*QueueDepthResponse, error)
Close() error
```

**API Gateway Integration** (`pkg/api/handlers.go`):

```go
// HandleCreateTask validates request, calls Module 1, then creates CloudTask
HandleCreateTask(w http.ResponseWriter, r *http.Request)
  1. Validate fields (name, tenant_id, image)
  2. Call Module1Client.SubmitTask()
  3. If 503: Return error to client, DO NOT create CloudTask
  4. If success: Create CloudTask in Kubernetes, return 202
```

### Testing

**Unit Tests:** `pkg/grpc/module1_client_test.go`

```bash
# Run all gRPC tests
go test ./pkg/grpc/... -v

# Run with coverage
go test ./pkg/grpc/... -cover

# Run specific test
go test ./pkg/grpc/module1_client_test.go -v -run TestCircuitBreaker
```

**Test Cases (10 total):**
- ✅ Submit task success
- ✅ Submit task failure
- ✅ Retry logic on transient failure
- ✅ Circuit breaker opens after max failures
- ✅ Circuit breaker recovers after timeout
- ✅ Timeout handling
- ✅ Get task status
- ✅ Cancel task
- ✅ Get queue depth
- ✅ Concurrent access

### Switching Between Real and Mock

```bash
# Switch to Mock (for testing without real Module 1)
./scripts/switch-to-mock.sh mock

# Switch to Real (production)
./scripts/switch-to-mock.sh real module1-scheduler:50051

# Check current mode
./scripts/switch-to-mock.sh status
```

### Monitoring Health

```bash
# Watch health checks (every 30 seconds)
kubectl logs -f deployment/api-gateway -n orchestrator-system | \
  grep -i "health"

# Watch circuit breaker state
kubectl logs -f deployment/api-gateway -n orchestrator-system | \
  grep -i "circuit"

# Watch retry attempts
kubectl logs -f deployment/api-gateway -n orchestrator-system | \
  grep -i "retry"
```

---

## RBAC & Multi-Tenancy

### Namespace Structure

```
orchestrator-system/  (operator namespace)
  └── Operator deployment, API Gateway, Redis, PostgreSQL, Module 1 Service

tenant-a/  (tenant-specific namespace)
  └── Pods created for tenant-a tasks

tenant-b/  (another tenant namespace)
  └── Pods created for tenant-b tasks
```

### RBAC Configuration

**Files:**
- `config/rbac/rbac.yaml` - Operator service account and permissions
- `config/rbac/namespace-template.yaml` - Template for new tenants
- `config/rbac/namespace-tenant-a.yaml` - Tenant-a specific config
- `config/rbac/tenant-role.yaml` - Role for tenant pods
- `config/rbac/tenant-rolebinding.yaml` - Role binding for tenant pods

### Create New Tenant

```bash
# Automated script
./scripts/create-tenant.sh tenant-c

# Or manually:
# 1. Create namespace
kubectl create namespace tenant-c

# 2. Apply tenant config
kubectl apply -f config/rbac/namespace-template.yaml \
  -n tenant-c

# 3. Apply role and binding
kubectl apply -f config/rbac/tenant-role.yaml \
  -n tenant-c
kubectl apply -f config/rbac/tenant-rolebinding.yaml \
  -n tenant-c

# 4. Verify
kubectl get namespace tenant-c
kubectl get roles -n tenant-c
kubectl get rolebindings -n tenant-c
```

### Tenant Isolation

**Network Policies** (`config/networking/network-policy.yaml`):
- Pods within a tenant can communicate
- Cross-tenant communication blocked
- External traffic controlled by ingress

**Resource Quotas** (`config/rbac/resourcequota.yaml`):
- CPU limits per tenant
- Memory limits per tenant
- Pod count limits per tenant

**Verify Isolation:**
```bash
# List tenants
./scripts/list-tenants.sh

# Test tenant isolation
./scripts/validate-rbac.sh

# Test cross-tenant communication (should fail)
kubectl exec -it pod/task-x -n tenant-a -- \
  ping pod-in-tenant-b.tenant-b
# Should either timeout or be refused
```

### Delete Tenant

```bash
# Automated script
./scripts/delete-tenant.sh tenant-c

# Or manually:
kubectl delete namespace tenant-c
```

---

## Redis & Pub/Sub

### Redis Architecture

```
┌─────────────────────────────────┐
│         Redis Instance          │
│                                 │
│  Channels:                      │
│  ├── task:completed             │
│  │   └─ CloudTask status update │
│  ├── task:failed                │
│  │   └─ CloudTask error update  │
│  └── deployment:events          │
│      └─ Deployment notifications│
│                                 │
│  Persistence:                   │
│  └── RDB snapshots (hourly)     │
└─────────────────────────────────┘
```

### Event Schema

**Task Completed Event:**
```json
{
  "type": "task:completed",
  "timestamp": "2026-04-20T10:35:00Z",
  "task_id": "task-abc123",
  "status": "succeeded",
  "duration_seconds": 300,
  "output": "task result"
}
```

**Task Failed Event:**
```json
{
  "type": "task:failed",
  "timestamp": "2026-04-20T10:35:00Z",
  "task_id": "task-abc123",
  "status": "failed",
  "error": "Out of memory",
  "retrying": true,
  "retry_count": 2
}
```

### Subscribing to Events

**Operator Controller:**
```go
// controllers/cloudtask_controller.go
Subscribe to:
  - task:completed    → Update CloudTask status to "Succeeded"
  - task:failed       → Update CloudTask status to "Failed", log error
  - task:log_output   → Append to pod logs
```

### Configuration

**File:** `config/redis/redis-configmap.yaml`

```yaml
parameters: |
  maxmemory 512mb
  maxmemory-policy allkeys-lru
  save "3600 1"  # RDB snapshot every hour
  appendonly yes  # AOF persistence
```

### Health Check

```bash
# Check Redis connection
kubectl exec -it pod/redis-0 -n orchestrator-system -- redis-cli ping
# Should return: PONG

# Monitor pub/sub traffic
kubectl exec -it pod/redis-0 -n orchestrator-system -- redis-cli
> MONITOR
> (in another window) PUBLISH task:completed '{"task_id":"test"}'

# Check memory usage
kubectl exec -it pod/redis-0 -n orchestrator-system -- redis-cli INFO memory
```

---

## Deployment

### Build All Images

```bash
# Build all Docker images
docker build -f Dockerfile.operator -t cloudtask-operator:latest .
docker build -f Dockerfile.gateway -t cloudtask-api-gateway:latest .
docker build -f Dockerfile.build -t cloudtask-build:latest .

# Or use Makefile
make build-images
```

### Deploy to Kubernetes

```bash
# Full deployment (includes all components)
make deploy-all

# Or step-by-step:
kubectl apply -f config/crd/cloudtask_crd.yaml
kubectl apply -f config/rbac/rbac.yaml
kubectl apply -f config/postgres/
kubectl apply -f config/redis/
kubectl apply -f config/operator/deployment.yaml
```

### Verify Deployment

```bash
# Check all pods are running
kubectl get pods -n orchestrator-system

# Check services are available
kubectl get svc -n orchestrator-system

# Check CRD is installed
kubectl get crds | grep cloudtask

# Test API Gateway endpoint
kubectl port-forward svc/api-gateway 8080:8080 -n orchestrator-system &
curl http://localhost:8080/api/v1/health
```

### Update Deployment

```bash
# Update operator image
kubectl set image deployment/operator \
  operator=cloudtask-operator:v2.0 \
  -n orchestrator-system

# Verify rollout
kubectl rollout status deployment/operator -n orchestrator-system

# Rollback if needed
kubectl rollout undo deployment/operator -n orchestrator-system
```

---

## Troubleshooting

### Circuit Breaker Constantly Open

**Symptoms:**
- All task submissions return 503
- Logs show "circuit breaker open"

**Solutions:**
```bash
# 1. Check Module 1 connectivity
kubectl get svc -n orchestrator-system module1-scheduler

# 2. Test gRPC connection
kubectl run -it --rm debug --image=busybox --restart=Never -- \
  nc -zv module1-scheduler 50051

# 3. Check Module 1 logs (if available)
kubectl logs -f deployment/module1-scheduler -n module1-ns

# 4. Increase timeout
export MODULE1_GRPC_TIMEOUT="60s"

# 5. Switch to mock to isolate issue
./scripts/switch-to-mock.sh mock

# 6. Monitor recovery
kubectl logs -f deployment/api-gateway -n orchestrator-system | \
  grep -i "circuit\|health"
```

### Pods Not Creating

**Symptoms:**
- CloudTask created but no Pod spawned
- Operator logs show errors

**Solutions:**
```bash
# 1. Check operator logs
kubectl logs -f deployment/operator -n orchestrator-system

# 2. Verify CRD installed
kubectl get crd | grep cloudtask

# 3. Check RBAC permissions
kubectl auth can-i create pods --as=system:serviceaccount:orchestrator-system:operator-sa

# 4. Check namespace exists
kubectl get ns | grep tenant-

# 5. Check resource quotas
kubectl describe quota -n tenant-a

# 6. Describe CloudTask for errors
kubectl describe cloudtask task-abc123 -n tenant-a
```

### PostgreSQL Connection Failed

**Symptoms:**
- Task history not saved
- Operator logs show database errors

**Solutions:**
```bash
# 1. Check PostgreSQL pod
kubectl get pods -n orchestrator-system -l app=postgres

# 2. Test connection
kubectl exec -it pod/postgres-0 -n orchestrator-system -- \
  psql -U postgres -c "SELECT 1"

# 3. Check logs
kubectl logs -f pod/postgres-0 -n orchestrator-system

# 4. Verify PVC is mounted
kubectl get pvc -n orchestrator-system

# 5. Restart PostgreSQL
kubectl delete pod postgres-0 -n orchestrator-system
# (StatefulSet will recreate)
```

### Redis Connection Failed

**Symptoms:**
- Task completion events not published
- Operator logs show Redis errors

**Solutions:**
```bash
# 1. Check Redis pod
kubectl get pods -n orchestrator-system -l app=redis

# 2. Test connection
kubectl exec -it pod/redis-0 -n orchestrator-system -- redis-cli ping

# 3. Check logs
kubectl logs -f pod/redis-0 -n orchestrator-system

# 4. Check memory usage
kubectl exec -it pod/redis-0 -n orchestrator-system -- \
  redis-cli INFO memory

# 5. Check pub/sub subscriptions
kubectl exec -it pod/redis-0 -n orchestrator-system -- \
  redis-cli PUBSUB CHANNELS
```

### High Latency

**Symptoms:**
- Task submission takes >500ms
- Timeout errors appear occasionally

**Solutions:**
```bash
# 1. Monitor Module 1 queue depth
kubectl logs -f deployment/api-gateway -n orchestrator-system | \
  grep "queue_depth"

# 2. Check resource usage
kubectl top pods -n orchestrator-system
kubectl top nodes

# 3. Increase timeout
export MODULE1_GRPC_TIMEOUT="60s"

# 4. Check network latency
kubectl exec -it pod/api-gateway -n orchestrator-system -- \
  ping module1-scheduler

# 5. Scale up pods
kubectl scale deployment/operator --replicas=3 -n orchestrator-system
```

### Common Errors

**Error: "unable to connect to server"**
```bash
# Solution: Verify cluster is running
docker ps | grep kind
kind get clusters
```

**Error: "namespace does not exist"**
```bash
# Solution: Create namespace
kubectl create namespace orchestrator-system
```

**Error: "CRD not found"**
```bash
# Solution: Apply CRD
kubectl apply -f config/crd/cloudtask_crd.yaml
```

**Error: "Port already in use"**
```bash
# Solution: Use different port
kind create cluster --name orchestrator2 --image kindest/node:v1.28.0

# Or kill process using port
lsof -i :8080
kill -9 <PID>
```

---

## Available Make Commands

```bash
# Build
make build              # Build operator and gateway
make build-images       # Build Docker images
make docker-build       # Docker build with caching

# Test
make test              # Run all tests
make test-unit         # Run unit tests only
make test-coverage      # Run tests with coverage report

# Deploy
make deploy            # Deploy to kubernetes
make deploy-all        # Deploy all components
make kind-create       # Create kind cluster
make kind-delete       # Delete kind cluster

# Clean
make clean             # Clean build artifacts
make clean-all         # Clean and delete cluster

# Utility
make help              # Show all available commands
```

---

## File Structure

```
module2-orchestrator/
├── cmd/
│   ├── api-gateway/main.go        (API server)
│   └── operator/main.go           (K8s operator)
├── pkg/
│   ├── api/handlers.go            (HTTP handlers)
│   ├── auth/jwt.go                (JWT authentication)
│   ├── grpc/
│   │   ├── module1_client.go       (Real Module 1 gRPC client)
│   │   ├── module1_client_test.go  (Unit tests)
│   │   └── proto/
│   │       ├── scheduler.pb.go           (Protobuf messages)
│   │       └── scheduler_grpc.pb.go      (gRPC stubs)
│   ├── controller/                (unused)
│   ├── operator/                  (unused)
│   ├── pubsub/                    (Redis pub/sub)
│   ├── scaling/                   (autoscaling logic)
│   ├── storage/                   (database access)
│   └── utils/                     (helper functions)
├── controllers/
│   ├── cloudtask_controller.go    (Main reconciliation logic)
│   ├── cloudtask_init_test.go     (Basic tests)
│   └── cloudtask_controller_test_basic.go (Integration tests)
├── api/v1/
│   ├── cloudtask_types.go         (CRD definition)
│   ├── cloudtask_webhook.go       (Validation webhooks)
│   └── zz_generated.deepcopy.go   (Generated code)
├── config/
│   ├── crd/cloudtask_crd.yaml     (Custom Resource Definition)
│   ├── rbac/rbac.yaml             (RBAC rules)
│   ├── operator/deployment.yaml   (Operator deployment)
│   ├── postgres/                  (PostgreSQL manifests)
│   ├── redis/                     (Redis manifests)
│   ├── integration/module1-service.yaml (Module 1 service)
│   └── kind/cluster.yaml          (Kind cluster config)
├── scripts/
│   ├── setup-kind.sh              (Create Kind cluster)
│   ├── setup-redis.sh             (Deploy Redis)
│   ├── setup-monitoring.sh        (Deploy monitoring)
│   ├── integration-test.sh        (E2E tests)
│   ├── switch-to-mock.sh          (Mock/real toggle)
│   ├── create-tenant.sh           (Create tenant namespace)
│   ├── delete-tenant.sh           (Delete tenant namespace)
│   ├── list-tenants.sh            (List all tenants)
│   ├── validate-rbac.sh           (Validate RBAC rules)
│   └── deploy-rbac.ps1            (PowerShell RBAC deploy)
├── Dockerfile.operator            (Operator image)
├── Dockerfile.gateway             (API Gateway image)
├── Dockerfile.build               (Build tools image)
├── Makefile                       (Build rules)
├── go.mod                         (Go dependencies)
├── DOCUMENTATION.md               (This file)
└── README.md                      (Quick reference)
```

---

## Performance Characteristics

### Latency

| Operation | Typical | Max |
|-----------|---------|-----|
| Task submission | 50-200ms | 500ms (with retries) |
| Task status fetch | 30-100ms | 200ms |
| Pod creation | 2-5s | 10s |
| E2E (submit → complete) | 4-10s | 30s |

### Throughput

| Metric | Value |
|--------|-------|
| Tasks per second | 10-50 (depends on Module 1) |
| Concurrent pods | Limited by node resources |
| API requests/sec | 100+ |

### Resource Usage (Per Pod)

| Resource | Typical | Max |
|----------|---------|-----|
| CPU | 100m | 500m |
| Memory | 64Mi | 256Mi |
| Network | 1-5 Mb/s | 20 Mb/s |

---

## Support & Troubleshooting

### Getting Help

1. **Check logs:**
   ```bash
   kubectl logs -f deployment/operator -n orchestrator-system
   ```

2. **Describe resources:**
   ```bash
   kubectl describe cloudtask <task-id> -n <tenant>
   ```

3. **Examine events:**
   ```bash
   kubectl get events -n orchestrator-system --sort-by='.lastTimestamp'
   ```

4. **Test connectivity:**
   ```bash
   kubectl exec -it pod/debug -n orchestrator-system -- /bin/sh
   ```

### Debug Mode

Enable debug logging:
```bash
export LOG_LEVEL=debug
kubectl set env deployment/operator LOG_LEVEL=debug -n orchestrator-system
```

### Common Issues & Solutions

See [Troubleshooting](#troubleshooting) section above for detailed solutions to common errors.

---

## Version Information

- **Project:** Module 2 Orchestrator
- **Version:** 1.0.0
- **Kubernetes:** 1.28+
- **Go:** 1.20+
- **Docker:** 20.10+
- **Module 1 Integration:** Complete with Circuit Breaker, Retry Logic, Health Checks

---

**Last Updated:** April 2026  
**Maintained By:** CloudTask Team
