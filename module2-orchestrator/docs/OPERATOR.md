# CloudTask Operator Documentation

## Table of Contents

1. [Overview](#overview)
2. [Installation](#installation)
3. [CloudTask Examples](#cloudtask-examples)
4. [Lifecycle Diagram](#lifecycle-diagram)
5. [Debugging Commands](#debugging-commands)
6. [Metrics Reference](#metrics-reference)
7. [Configuration](#configuration)
8. [Troubleshooting](#troubleshooting)
9. [Advanced Features](#advanced-features)
10. [Next Steps](#next-steps)

## Overview

The CloudTask Operator is a Kubernetes controller that manages the lifecycle of CloudTask resources. It implements the following responsibilities:

- **Reconciliation**: Watches CloudTask resources and ensures desired state
- **Pod Management**: Creates and manages Kubernetes Pods based on CloudTask spec
- **Status Tracking**: Updates CloudTask status based on Pod phase
- **Retry Logic**: Automatically retries failed tasks up to spec.retries
- **Health Checks**: Regular health checks and graceful degradation
- **Metrics**: Exports Prometheus metrics for monitoring

### Architecture Components

```
CloudTaskReconciler
├── Reconcile() - Main loop, watches CloudTasks
├── updateStatusFromPod() - Sync CloudTask status with Pod phase
├── constructPod() - Build Pod spec from CloudTask spec
└── deleteExternalResources() - Cleanup on deletion
```

## Installation

### Prerequisites

- Kubernetes 1.28+ cluster
- kubectl configured to access cluster
- Docker (for building images)

### Step 1: Create Namespace

```bash
kubectl create namespace orchestrator-system
kubectl create namespace monitoring
```

### Step 2: Apply CRD

```bash
kubectl apply -f config/crd/cloudtask_crd.yaml
```

Verify CRD:
```bash
kubectl get crd cloudtasks.tasks.orchestrator.dev
```

### Step 3: Apply RBAC

```bash
kubectl apply -f config/rbac/rbac.yaml
```

Verify RBAC:
```bash
kubectl get serviceaccount -n orchestrator-system
kubectl get clusterrole | grep cloudtask
```

### Step 4: Deploy Operator

```bash
# Build and push Docker image
docker build -t your-registry/cloudtask-operator:v1.0.0 -f Dockerfile.operator .
docker push your-registry/cloudtask-operator:v1.0.0

# Update image in deployment
kubectl set image deployment/cloudtask-operator \
  cloudtask-operator=your-registry/cloudtask-operator:v1.0.0 \
  -n orchestrator-system

# Or apply directly
kubectl apply -f config/operator/deployment.yaml
```

Verify operator:
```bash
kubectl get deployment -n orchestrator-system
kubectl logs -f deployment/cloudtask-operator -n orchestrator-system
```

### Step 5: Install Monitoring (Optional)

```bash
kubectl apply -f config/monitoring/prometheus.yaml
```

## CloudTask Examples

### Example 1: Simple Task

```yaml
apiVersion: tasks.orchestrator.dev/v1
kind: CloudTask
metadata:
  name: hello-world
  namespace: default
spec:
  image: alpine:latest
  command:
    - /bin/sh
    - -c
  args:
    - "echo 'Hello from CloudTask' && sleep 10"
  tenantID: tenant-1
  timeout: "5m"
```

Create and monitor:
```bash
kubectl apply -f - <<EOF
apiVersion: tasks.orchestrator.dev/v1
kind: CloudTask
metadata:
  name: hello-world
spec:
  image: alpine:latest
  command:
    - sh
    - -c
  args:
    - "echo 'Hello' && sleep 10"
  tenantID: tenant-1
EOF

kubectl get cloudtasks hello-world -w
kubectl describe cloudtask hello-world
```

### Example 2: Task with Resources

```yaml
apiVersion: tasks.orchestrator.dev/v1
kind: CloudTask
metadata:
  name: data-processor
  namespace: default
spec:
  image: python:3.11-slim
  command:
    - python
    - /app/process.py
  tenantID: tenant-1
  retries: 3
  timeout: "30m"
  priority: 75
  resources:
    requests:
      cpu: "250m"
      memory: "256Mi"
    limits:
      cpu: "1000m"
      memory: "1024Mi"
```

### Example 3: Task with Retry Logic

```yaml
apiVersion: tasks.orchestrator.dev/v1
kind: CloudTask
metadata:
  name: reliable-task
  namespace: default
spec:
  image: busybox:latest
  command:
    - sh
    - -c
  args:
    - "echo 'Attempt'; test $RANDOM -lt 20000 && exit 1 || exit 0"
  tenantID: tenant-1
  retries: 5           # Will retry up to 5 times
  timeout: "10m"
  priority: 50
```

### Example 4: Task with Environment Variables

```yaml
apiVersion: tasks.orchestrator.dev/v1
kind: CloudTask
metadata:
  name: env-task
  namespace: default
spec:
  image: bash:latest
  command:
    - bash
    - -c
  args:
    - |
      echo "API Key: $API_KEY"
      echo "Database: $DB_HOST:$DB_PORT"
      echo "Processing job: $JOB_ID"
  tenantID: tenant-1
  env:
    - name: API_KEY
      value: "secret123"
    - name: DB_HOST
      value: "postgres.default.svc.cluster.local"
    - name: DB_PORT
      value: "5432"
    - name: JOB_ID
      value: "job-12345"
```

### Example 5: Task with Labels and Annotations

```yaml
apiVersion: tasks.orchestrator.dev/v1
kind: CloudTask
metadata:
  name: production-task
  namespace: default
  labels:
    env: production
    team: platform
spec:
  image: node:18-alpine
  command:
    - node
    - /app/index.js
  tenantID: tenant-1
  retries: 3
  timeout: "1h"
  priority: 90
  labels:
    workload: batch-job
    priority: high
  annotations:
    owner: platform-team
    cost-center: engineering
    runbook: "https://wiki.example.com/batch-jobs"
```

## Lifecycle Diagram

### Complete Task Lifecycle

```
┌─────────────────────────────────────────────────────────────┐
│ 1. CloudTask Created                                        │
│    kubectl apply -f cloudtask.yaml                          │
└──────────────┬──────────────────────────────────────────────┘
               │
               ▼
┌─────────────────────────────────────────────────────────────┐
│ 2. Finalizer Added                                          │
│    - Operator adds finalizer: tasks.orchestrator.dev/...    │
│    - Ensures cleanup on deletion                            │
│    - Status.phase = "Pending"                               │
└──────────────┬──────────────────────────────────────────────┘
               │
               ▼
┌─────────────────────────────────────────────────────────────┐
│ 3. Pod Created                                              │
│    - Operator creates Pod with CloudTask spec               │
│    - Pod name: <cloudtask-name>-pod                         │
│    - Labels: cloudtask-name, tenant-id, priority            │
│    - RestartPolicy: Never                                   │
│    - Status.phase = "Running"                               │
└──────────────┬──────────────────────────────────────────────┘
               │
               ▼
┌─────────────────────────────────────────────────────────────┐
│ 4. Pod Running                                              │
│    - Container executes task                                │
│    - Logs available via kubectl logs                        │
│    - Operator monitors Pod status                           │
│    - Status.startTime recorded                              │
└──────────────┬──────────────────────────────────────────────┘
               │
        ┌──────┴──────┐
        │             │
        ▼             ▼
     ┌─────┐      ┌──────┐
     │Pass │      │Fail  │
     └──┬──┘      └──┬───┘
        │            │
        ▼            ▼
    ┌────────────────────────────┐
    │ 5. Status Updated         │
    │    - Phase = Completed    │
    │    - Phase = Failed       │
    │    - CompletionTime set   │
    │    - Message updated      │
    └────────────┬───────────────┘
                 │
         ┌───────┴────────┐
         │                │
         ▼                ▼
    ┌─────────────┐   ┌─────────────┐
    │ Completed   │   │  Retry?     │
    │ (Success)   │   │  (Failed)   │
    └─────────────┘   └──────┬──────┘
                             │
                      ┌──────▼──────┐
                      │ More retries│
                      │ available?  │
                      └──────┬──────┘
                          ┌──┴──┐
                       Yes│     │No
                          ▼     ▼
                      ┌──────┐  ┌──────────┐
                      │Retry │  │Final Fail│
                      │      │  │Failed    │
                      └──────┘  └──────────┘
```

### Retry State Machine

```
Initial Pod Fails (retryCount = 0, retriesRemaining = 3)
|
├─> Update Status to Failed
├─> Increment retryCount (now = 1)
├─> Decrement retriesRemaining (now = 2)
├─> Set Status.phase = Pending
├─> Clear Status.podName (delete old Pod)
├─> Update Status.message = "Retrying (attempt 1/3)"
├─> Requeue with 5-second delay
|
▼
Next Reconciliation triggers
|
├─> New Pod created
├─> Status.phase = Running
├─> Cycle repeats...
```

## Debugging Commands

### View CloudTasks

```bash
# List all CloudTasks
kubectl get cloudtasks

# List with more details
kubectl get cloudtasks -o wide

# Show all columns including status
kubectl get cloudtasks -o custom-columns=NAME:.metadata.name,PHASE:.status.phase,POD:.status.podName,RETRIES:.status.retryCount

# Watch for changes
kubectl get cloudtasks --watch

# Get CloudTask in specific namespace
kubectl get cloudtasks -n tenant-acme

# Get CloudTasks for specific tenant
kubectl get cloudtasks -l tenant-id=tenant-1
```

### Describe CloudTasks

```bash
# Full details of a CloudTask
kubectl describe cloudtask simple-task

# JSON format
kubectl get cloudtask simple-task -o json

# YAML format
kubectl get cloudtask simple-task -o yaml

# Show events related to CloudTask
kubectl describe cloudtask simple-task | grep -A 20 Events:
```

### View Pod Status

```bash
# List all Pods created by CloudTasks
kubectl get pods -l cloudtask-name

# Show Pod details
kubectl describe pod simple-task-pod

# View Pod logs
kubectl logs simple-task-pod

# Stream logs in real-time
kubectl logs -f simple-task-pod

# View logs with timestamps
kubectl logs simple-task-pod --timestamps=true

# Get last N lines
kubectl logs simple-task-pod --tail=100
```

### View Operator Logs

```bash
# Real-time logs
kubectl logs -f deployment/cloudtask-operator -n orchestrator-system

# Last 100 lines
kubectl logs deployment/cloudtask-operator -n orchestrator-system --tail=100

# Filter by severity
kubectl logs deployment/cloudtask-operator -n orchestrator-system | grep ERROR
kubectl logs deployment/cloudtask-operator -n orchestrator-system | grep WARN
kubectl logs deployment/cloudtask-operator -n orchestrator-system | grep "test-task"

# Previous container logs (if crashed)
kubectl logs deployment/cloudtask-operator -n orchestrator-system --previous
```

### Debug Pod

```bash
# Debug a running Pod
kubectl debug pod/simple-task-pod -it --image=busybox

# Get Pod events
kubectl get events --field-selector involvedObject.name=simple-task-pod

# Check Pod resources
kubectl top pod simple-task-pod
```

### Operator Status

```bash
# Check operator deployment
kubectl get deployment cloudtask-operator -n orchestrator-system

# Check operator pod
kubectl get pods -n orchestrator-system

# Get operator metrics
kubectl get endpoints -n orchestrator-system

# Check service accounts
kubectl get serviceaccount -n orchestrator-system

# Check RBAC
kubectl auth can-i list cloudtasks --as=system:serviceaccount:orchestrator-system:cloudtask-operator
```

## Metrics Reference

### Exported Metrics (Prometheus)

```
# HELP cloudtask_reconcile_duration_seconds Duration of CloudTask reconciliation
# TYPE cloudtask_reconcile_duration_seconds histogram
cloudtask_reconcile_duration_seconds_bucket{le="0.005"} 10
cloudtask_reconcile_duration_seconds_bucket{le="0.01"} 25
cloudtask_reconcile_duration_seconds_bucket{le="0.1"} 100
cloudtask_reconcile_duration_seconds_bucket{le="+Inf"} 125
cloudtask_reconcile_duration_seconds_sum 3.14
cloudtask_reconcile_duration_seconds_count 125

# HELP cloudtask_total_count Total number of CloudTasks
# TYPE cloudtask_total_count gauge
cloudtask_total_count{phase="Pending"} 5
cloudtask_total_count{phase="Running"} 12
cloudtask_total_count{phase="Completed"} 1200
cloudtask_total_count{phase="Failed"} 45

# HELP cloudtask_pod_creation_total Total Pods created
# TYPE cloudtask_pod_creation_total counter
cloudtask_pod_creation_total 1245

# HELP cloudtask_retries_total Total retries executed
# TYPE cloudtask_retries_total counter
cloudtask_retries_total{status="success"} 350
cloudtask_retries_total{status="exhausted"} 20

# HELP cloudtask_task_duration_seconds Task execution duration
# TYPE cloudtask_task_duration_seconds histogram
cloudtask_task_duration_seconds_bucket{le="1"} 100
cloudtask_task_duration_seconds_bucket{le="10"} 200
cloudtask_task_duration_seconds_bucket{le="60"} 300
cloudtask_task_duration_seconds_bucket{le="+Inf"} 350
```

### Queries for Prometheus

```promql
# Tasks by phase
sum(cloudtask_total_count) by (phase)

# Failure rate (5-minute window)
sum(rate(cloudtask_total_count{phase="Failed"}[5m])) / sum(rate(cloudtask_total_count[5m]))

# Average reconciliation time
histogram_quantile(0.95, cloudtask_reconcile_duration_seconds)

# Pending tasks
cloudtask_total_count{phase="Pending"}

# Successful retries
rate(cloudtask_retries_total{status="success"}[5m])
```

## Configuration

### Operator Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `LOG_LEVEL` | `info` | Log level: debug, info, warn, error |
| `LEADER_ELECT` | `false` | Enable leader election for HA |
| `ENABLE_WEBHOOKS` | `false` | Enable validating/mutating webhooks |
| `METRICS_BIND_ADDRESS` | `:8080` | Metrics server bind address |
| `HEALTH_PROBE_PORT` | `:8081` | Health probe bind address |
| `KUBECONFIG` | `~/.kube/config` | Kubernetes config file path |

### CloudTask Defaults

| Field | Default | Notes |
|-------|---------|-------|
| `spec.retries` | 3 | Max 10, min 0 |
| `spec.priority` | 50 | Range 0-100 |
| `spec.timeout` | 5m | Format: \d+[smh] |
| `spec.resources` | None | Optional |
| `spec.env` | [] | Optional |
| `spec.labels` | {} | Optional, pod labels |
| `spec.annotations` | {} | Optional, pod annotations |

### Pod Spec Generated

```yaml
spec:
  restartPolicy: Never
  activeDeadlineSeconds: 300  # From spec.timeout if set
  terminationGracePeriodSeconds: 30
  containers:
  - name: task-container
    image: "{{ .Spec.Image }}"
    command: "{{ .Spec.Command }}"
    args: "{{ .Spec.Args }}"
    resources: "{{ .Spec.Resources }}"
    env: "{{ .Spec.Env }}"
    volumeMounts: "{{ .Spec.VolumeMounts }}"
```

## Troubleshooting

### CloudTask Stuck in Pending

```bash
# Check Pod status
kubectl describe pod simple-task-pod

# Common causes:
# 1. Image not found
# 2. Insufficient resources
# 3. Node selector not matching
# 4. Pod scheduling constraints

# Check events
kubectl get events --field-selector involvedObject.name=simple-task-pod

# Verify events on Operator
kubectl describe cloudtask simple-task | tail -20
```

### Pod Fails Immediately

```bash
# Check Pod logs
kubectl logs simple-task-pod

# Check exit code
kubectl get pod simple-task-pod -o jsonpath='{.status.containerStatuses[0].state.terminated.exitCode}'

# Check container status
kubectl describe pod simple-task-pod | grep -A 5 "Container ID"

# Common causes:
# - Script error (exit code > 0)
# - Image pull failure
# - Command not found
# - Permission denied
```

### Operator Not Reconciling

```bash
# Check operator pod
kubectl get pod -n orchestrator-system

# Check operator logs for errors
kubectl logs deployment/cloudtask-operator -n orchestrator-system | grep ERROR

# Verify RBAC
kubectl auth can-i list cloudtasks --as=system:serviceaccount:orchestrator-system:cloudtask-operator

# Check if webhook is blocking
kubectl get validatingwebhookconfiguration

# Restart operator
kubectl rollout restart deployment/cloudtask-operator -n orchestrator-system
```

### High Memory Usage

```bash
# Monitor operator memory
kubectl top pod -n orchestrator-system

# Check for memory leaks in logs
kubectl logs deployment/cloudtask-operator -n orchestrator-system | grep -i "memory\|heap"

# Reduce reconciliation frequency by adjusting RequeueAfter
# Default: 30 seconds
```

### Metrics Not Available

```bash
# Check metrics endpoint
kubectl port-forward -n orchestrator-system svc/cloudtask-operator-metrics 8080:8080
curl http://localhost:8080/metrics

# Verify ServiceMonitor (if using Prometheus Operator)
kubectl get servicemonitor -n orchestrator-system
```

## Advanced Features

### Multi-Tenancy

```bash
# Create tenant namespace
kubectl create namespace tenant-acme

# Resources are automatically isolated
kubectl get resourcequota -n tenant-acme

# Only tenant service account can manage tasks
kubectl --as=system:serviceaccount:tenant-acme:tenant-acme get cloudtasks -n tenant-acme
```

### Priority-Based Scheduling

Tasks with higher priority are processed first:

```yaml
spec:
  priority: 100  # Higher priority
  
spec:
  priority: 1    # Lower priority
```

### Task Timeout

Tasks are automatically terminated after timeout:

```yaml
spec:
  timeout: "10m"  # Formats: 30s, 5m, 1h
```

### Retry Policy

Failed tasks are automatically retried:

```yaml
spec:
  retries: 3  # Will be retried up to 3 times
```

Each retry increments `status.retryCount` and resets status to Pending.

## Next Steps

1. **Deploy to Production**: Use [DEPLOYMENT.md](DEPLOYMENT.md) for production guidelines
2. **Set Up Monitoring**: Follow [monitoring setup](SETUP.md#monitoring) for Prometheus integration
3. **Configure GitOps**: Integrate with [ArgoCD](SETUP.md#gitops)
4. **Integrate with CI/CD**: See [API documentation](API.md) for task submission
5. **Custom Webhooks**: Implement validation/mutation webhooks
6. **Auto-Scaling**: Configure KEDA for workload-aware scaling

## Support Resources

- GitHub Issues: https://github.com/orchestrator/module2-orchestrator/issues
- Discussions: https://github.com/orchestrator/module2-orchestrator/discussions
- Email: support@orchestrator.dev
- Slack: #cloudtask-users (internal)
