# CloudTask Orchestrator - Module 2

Production-ready Kubernetes task orchestration engine with Module 1 gRPC integration, multi-tenancy, auto-scaling, and monitoring.

**📖 Full Documentation:** [DOCUMENTATION.md](DOCUMENTATION.md)

---

## Quick Start (5 Minutes)

## Quick Start (5 Minutes)

### Prerequisites
- Docker Desktop running
- kubectl installed
- 20GB free disk, 4GB+ RAM

### Deploy

```bash
cd c:\Users\suraj\OneDrive\Desktop\scheduler\module2-orchestrator

# 1. Create Kind cluster
kind create cluster --name orchestrator --image kindest/node:v1.28.0 --wait 5m

# 2. Create namespace
kubectl create namespace orchestrator-system

# 3. Deploy infrastructure
kubectl apply -f config/crd/cloudtask_crd.yaml
kubectl apply -f config/rbac/rbac.yaml
kubectl apply -f config/postgres/
kubectl apply -f config/redis/
kubectl apply -f config/integration/module1-service.yaml

# 4. Build and deploy operator
docker build -f Dockerfile.operator -t cloudtask-operator:latest .
kind load docker-image cloudtask-operator:latest --name=orchestrator
kubectl apply -f config/operator/deployment.yaml

# 5. Test
./scripts/integration-test.sh
```

---

### Key Capabilities

- **Kubernetes-Native**: Built on Kubernetes Custom Resource Definitions (CRDs) and Operators
- **Multi-Tenant**: Full isolation and quota management per tenant
- **Auto-Scaling**: Integrated with KEDA and Horizontal Pod Autoscaler (HPA)
- **Module 1 Integration**: Real gRPC with circuit breaker, retry logic, health checks
- **High Availability**: Leadership election, health checks, and graceful degradation
- **Security**: RBAC, network policies, pod security policies, and JWT authentication
- **Observability**: Structured JSON logging, Redis pub/sub events

## Architecture

```
API Client
    │
    ├─► API Gateway (HTTP)
    │       │
    │       ├─► Module 1 Client (gRPC)
    │       │   ├─ Circuit Breaker
    │       │   ├─ Retry Logic
    │       │   └─ Health Checks
    │       │
    │       └─► Create CloudTask (Kubernetes)
    │
    ├─► Operator Watches CloudTask
    │   ├─ Create Pod
    │   ├─ Monitor Execution
    │   └─ Update Status
    │
    ├─► Redis Pub/Sub Events
    │   └─ Update Task Status
    │
    └─► PostgreSQL
        └─ Store Task History
```

## Key Directories

| Directory | Purpose |
|-----------|---------|
| `cmd/` | API Gateway, Operator main entry points |
| `controllers/` | Kubernetes operator reconciliation logic |
| `pkg/grpc/` | Module 1 integration (real gRPC client) |
| `pkg/api/` | HTTP API handlers |
| `config/crd/` | CloudTask Custom Resource Definition |
| `config/rbac/` | RBAC configuration for multi-tenancy |
| `config/postgres/` | PostgreSQL deployment manifests |
| `config/redis/` | Redis deployment manifests |
| `config/operator/` | Operator & API Gateway deployments |
| `config/integration/` | Module 1 service configuration |
| `scripts/` | Deployment and operational scripts |

---

## Module 1 Integration

**Status:** ✅ Complete with circuit breaker, retry logic, health checks

### Key Features
- Circuit breaker (5 failures → open, 30s recovery)
- Exponential backoff retry (1s → 2s → 4s)
- Health checks every 30 seconds
- Thread-safe concurrent access
- Structured logging

### Configuration
```bash
export MODULE1_GRPC_ADDRESS="module1-scheduler:50051"    # gRPC endpoint
export MODULE1_GRPC_TIMEOUT="30s"                        # Call timeout
export MODULE1_GRPC_MAX_RETRIES="3"                      # Max retries
```

### Files
- Client: `pkg/grpc/module1_client.go`
- Tests: `pkg/grpc/module1_client_test.go` (10 test cases)
- Service: `config/integration/module1-service.yaml`

### Test
```bash
go test ./pkg/grpc/... -v
./scripts/integration-test.sh
./scripts/switch-to-mock.sh status
```

---

## Essential Scripts

| Script | Purpose |
|--------|---------|
| `setup-kind.sh` | Create Kind cluster |
| `setup-redis.sh` | Deploy Redis |
| `setup-monitoring.sh` | Deploy Prometheus/Grafana |
| `integration-test.sh` | Full E2E test (7 steps) |
| `switch-to-mock.sh` | Switch Module 1 between real/mock |
| `create-tenant.sh` | Create new tenant namespace |
| `list-tenants.sh` | List all tenant namespaces |
| `validate-rbac.sh` | Validate RBAC configuration |

---

## Multi-Tenancy

Create isolated namespaces for each tenant:

```bash
# Create tenant
./scripts/create-tenant.sh tenant-name

# List tenants
./scripts/list-tenants.sh

# Delete tenant
./scripts/delete-tenant.sh tenant-name

# Validate RBAC isolation
./scripts/validate-rbac.sh
```

---

## API Reference

### Submit Task
```bash
curl -X POST http://localhost:8080/api/v1/tasks \
  -H "Content-Type: application/json" \
  -d '{
    "name": "my-task",
    "tenant_id": "tenant-a",
    "image": "busybox:latest",
    "command": ["echo", "hello"]
  }'
```

**Response (202 Accepted):**
```json
{
  "task_id": "task-abc123",
  "status": "submitted",
  "queue_position": 3,
  "estimated_wait_seconds": 45
}
```

### Get Task Status
```bash
curl http://localhost:8080/api/v1/tasks/task-abc123
```

### List Tasks
```bash
curl http://localhost:8080/api/v1/tasks?tenant_id=tenant-a&status=running
```

---

## Troubleshooting

**Circuit Breaker Open?**
```bash
kubectl logs -f deployment/api-gateway -n orchestrator-system | grep -i circuit
export MODULE1_GRPC_TIMEOUT="60s"
./scripts/switch-to-mock.sh mock
```

**Pods Not Creating?**
```bash
kubectl logs -f deployment/operator -n orchestrator-system
kubectl describe cloudtask task-id -n tenant-name
```

**Redis Connection Failed?**
```bash
kubectl exec -it pod/redis-0 -n orchestrator-system -- redis-cli ping
```

**PostgreSQL Not Accessible?**
```bash
kubectl exec -it pod/postgres-0 -n orchestrator-system -- psql -U postgres -c "SELECT 1"
```

---

## Make Commands

```bash
make build              # Build binaries
make build-images       # Build Docker images
make test              # Run all tests
make test-unit         # Run unit tests only
make test-coverage      # Generate coverage report
make deploy            # Deploy to Kubernetes
make deploy-all        # Deploy all components
make kind-create       # Create Kind cluster
make kind-delete       # Delete Kind cluster
make clean             # Clean build artifacts
make help              # Show all commands
```

---

## Project Structure
