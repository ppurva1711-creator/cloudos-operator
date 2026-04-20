# ☸️ CloudOS Scheduler

> A production-grade distributed task scheduling platform that mirrors Operating System scheduling internals at cloud scale inside Kubernetes.

![Status](https://img.shields.io/badge/status-in--progress-orange)
![Go](https://img.shields.io/badge/go-1.23+-00acd7)
![Kubernetes](https://img.shields.io/badge/kubernetes-1.28+-326ce5)
![License](https://img.shields.io/badge/license-MIT-green)

---

## 🧠 What Is This?

CloudOS Scheduler is a distributed task scheduling system that takes classic **Operating System scheduling concepts** — process queues, priority scheduling, deadlock detection, IPC — and implements them at cloud scale using **Kubernetes**.

Every "process" is a containerized pod. Every scheduling decision mirrors what a real OS kernel does. The result is a fully observable, auto-scaling task execution platform.

---

## 🏗️ Architecture Overview

```
┌─────────────────────────────────────────────────────────┐
│                    CLIENT LAYER                         │
│         React Dashboard  │  CLI Tool  │  Python SDK     │
└──────────────────────────┬──────────────────────────────┘
                           │ HTTPS / gRPC
┌──────────────────────────▼──────────────────────────────┐
│                    API GATEWAY                          │
│         FastAPI  │  JWT Auth  │  RBAC  │  Rate Limiter  │
└──────────────────────────┬──────────────────────────────┘
                           │ Task Submission
┌──────────────────────────▼──────────────────────────────┐
│                 SCHEDULER CORE (OS Brain)               │
│      Priority Queue  │  Deadlock Detector  │  Allocator │
│         MLFQ  │  Round Robin  │  SJF  │  FIFO          │
└──────────────────────────┬──────────────────────────────┘
                           │ Pod Spec → Controller
┌──────────────────────────▼──────────────────────────────┐
│              ☸ KUBERNETES CLUSTER                       │
│   Custom Operator (CRD: TaskJob)  │  HPA / KEDA         │
│   Worker Pod A  │  Worker Pod B  │  Worker Pod C        │
└────────────┬─────────────────────────────┬──────────────┘
             │ Pub/Sub · Streams            │ Metrics Scrape
┌────────────▼────────────┐   ┌────────────▼──────────────┐
│   STORAGE & IPC         │   │   OBSERVABILITY           │
│  Redis  │ etcd │ Postgres│   │  Prometheus │ Grafana │ELK│
└─────────────────────────┘   └───────────────────────────┘
```

---

## 🗂️ Project Structure

```
cloudos-operator/
├── api/v1/
│   ├── taskjob_types.go          # CRD schema — TaskJob custom resource
│   ├── groupversion_info.go      # API group registration
│   └── zz_generated.deepcopy.go # Auto-generated deep copy methods
│
├── internal/controller/
│   ├── taskjob_controller.go     # Main reconcile loop
│   ├── pod_builder.go            # Builds K8s Pod from TaskJob spec
│   └── status_sync.go            # Syncs pod phase → TaskJob status
│
├── api-gateway/
│   ├── main.py                   # FastAPI app entry point
│   ├── routers/
│   │   ├── tasks.py              # Task CRUD endpoints
│   │   ├── auth.py               # Login / JWT endpoints
│   │   └── workers.py            # Worker status endpoints
│   ├── middleware/
│   │   ├── auth.py               # JWT validation
│   │   ├── rate_limit.py         # Token bucket rate limiter
│   │   └── rbac.py               # Role-based access control
│   └── models/
│       ├── task.py               # Pydantic task schemas
│       └── user.py               # User/auth schemas
│
├── scheduler/
│   ├── priority_queue.py         # MLFQ, Round Robin, SJF, FIFO
│   ├── deadlock_detector.py      # DAG cycle detection
│   └── resource_allocator.py     # CPU/Memory bin packing
│
├── worker/
│   ├── Dockerfile                # Worker pod container image
│   └── runner.py                 # Task executor inside container
│
├── ui/
│   └── cloudos-ui.jsx            # React dashboard (all features)
│
├── config/
│   ├── crd/bases/                # Auto-generated CRD YAML
│   ├── rbac/                     # ServiceAccount, ClusterRole, Bindings
│   ├── manager/                  # Operator deployment YAML
│   └── samples/                  # Sample TaskJob manifests
│
├── monitoring/
│   ├── prometheus/               # Scrape configs and alert rules
│   └── grafana/                  # Dashboard JSON files
│
├── Dockerfile                    # Operator container image
├── Makefile                      # Build, generate, deploy commands
├── go.mod                        # Go module definition
└── README.md                     # This file
```

---

## 🔁 OS Concepts → Real Implementation

| OS Concept | How It's Applied | Implementation |
|---|---|---|
| **Process Scheduling** | Tasks = processes. Scheduler picks which pod runs next based on priority and burst time | MLFQ in Python, K8s PriorityClass |
| **Context Switching** | Pausing a running task, saving state, resuming higher-priority task | CRIU + K8s checkpointing API |
| **Memory Management** | Resource requests/limits on pods simulate OS memory allocation | K8s LimitRange, ResourceQuota |
| **Deadlock Detection** | Tasks waiting on each other's outputs can deadlock — detect cycles in DAG | DFS cycle detection on task graph |
| **Inter-Process Comm.** | Pods communicate results and share data via message queues | Redis Streams, gRPC, K8s Volumes |
| **Semaphores / Mutex** | Prevent concurrent writes to shared resources | Redlock algorithm via Redis |
| **Virtual Memory** | Tasks too large for one pod spill work to others | Task chunking + distributed map-reduce |
| **Signals (SIGTERM)** | Kubernetes sends SIGTERM for graceful pod shutdown | Python signal handlers, preStop hooks |

---

## 🧩 Layers — Build Progress

| Layer | Component | Status | Tech Stack |
|---|---|---|---|
| **Layer 1** | React UI Dashboard | ✅ Complete | React 18, D3.js, WebSocket |
| **Layer 2** | API Gateway | ✅ Complete | FastAPI, JWT, NGINX, Redis |
| **Layer 3** | Scheduler Core | 🔄 Planned | Python, MLFQ, networkx |
| **Layer 4** | Kubernetes Operator | 🔨 In Progress | Go, controller-runtime, KEDA |
| **Layer 5** | Storage & IPC | 🔄 Planned | Redis Streams, etcd, PostgreSQL |
| **Layer 6** | Observability | 🔄 Planned | Prometheus, Grafana, ELK |

---

## ⚙️ Layer 4 — Kubernetes Custom Operator (Current)

### Block 1 — Custom Operator ✅
The operator watches `TaskJob` custom resources and manages their full lifecycle.

**Custom Resource Example:**
```yaml
apiVersion: scheduler.cloudos.io/v1
kind: TaskJob
metadata:
  name: ml-training-v2
  namespace: cloudos
spec:
  name: ml-training-v2
  image: cloudos/worker:latest
  command: ["/bin/sh"]
  args: ["-c", "python train.py"]
  priority: high
  algorithm: mlfq
  cpuRequest: "2000m"
  memoryRequest: "2Gi"
  estimatedDurationSec: 300
  dependsOn:
    - data-prep-task
  env:
    - name: MODEL_TYPE
      value: "transformer"
```

**How it works:**
1. User submits a `TaskJob` YAML to the cluster
2. Custom Operator's `Reconcile()` loop detects it immediately
3. Checks all `dependsOn` tasks are completed
4. Creates a Kubernetes Pod with exact resource limits
5. Watches pod phase and mirrors it back to `TaskJob.status`
6. Pod cleans up automatically via OwnerReference when TaskJob is deleted

### Block 2 — HPA / KEDA Autoscaler 🔄 Coming Next
Scales worker pods up/down based on Redis queue depth.

### Block 3 — Worker Pods 🔄 Coming Next
Docker containers that execute task payloads and stream logs to Redis.

---

## 🚀 Quick Start

### Prerequisites
```bash
go version      # 1.23+
kubectl version # 1.28+
docker version  # 24+
kubebuilder version
make --version
```

### 1. Clone the repo
```bash
git clone https://github.com/yourGitHubUsername/cloudos-operator
cd cloudos-operator
```

### 2. Install dependencies
```bash
go mod tidy
```

### 3. Start local cluster
```bash
minikube start --cpus=4 --memory=4g
eval $(minikube docker-env)
```

### 4. Install CRD
```bash
make manifests
kubectl apply -f config/crd/bases/
```

### 5. Apply RBAC
```bash
kubectl create namespace cloudos
kubectl apply -f config/rbac/
```

### 6. Run operator locally (for development)
```bash
make run
```

### 7. Submit a test task
```bash
kubectl apply -f config/samples/sample-taskjob.yaml
kubectl get tj -n cloudos -w
```

---

## 🛠️ Makefile Commands

| Command | What It Does |
|---|---|
| `make generate` | Regenerates DeepCopy methods from type definitions |
| `make manifests` | Generates CRD YAML from kubebuilder markers |
| `make build` | Builds the operator binary |
| `make run` | Runs operator locally against current cluster |
| `make docker-build` | Builds Docker image for operator |
| `make deploy` | Deploys operator to cluster |
| `make install` | Installs CRDs into cluster |
| `make uninstall` | Removes CRDs from cluster |
| `make test` | Runs unit tests |

---

## 📡 API Endpoints (Layer 2 — API Gateway)

| Method | Endpoint | Description | Auth |
|---|---|---|---|
| `POST` | `/auth/login` | Get JWT token | None |
| `POST` | `/api/tasks` | Submit new task | JWT |
| `GET` | `/api/tasks` | List all tasks | JWT |
| `GET` | `/api/tasks/{id}` | Get task details | JWT |
| `DELETE` | `/api/tasks/{id}` | Cancel task | JWT + RBAC |
| `GET` | `/api/workers` | List worker pods | JWT |
| `GET` | `/health` | Health check | None |

---

## 🔧 Tech Stack

### Core
| Technology | Version | Purpose |
|---|---|---|
| Go | 1.23+ | Kubernetes Operator |
| Python | 3.12+ | API Gateway + Scheduler |
| React | 18 | Web Dashboard |

### Kubernetes
| Technology | Purpose |
|---|---|
| controller-runtime v0.17 | Operator framework |
| kubebuilder v4 | Operator scaffolding |
| KEDA | Event-driven autoscaling |
| Helm | Package deployment |

### Infrastructure
| Technology | Purpose |
|---|---|
| Redis 7 | Task queue + IPC + distributed locks |
| etcd | Cluster state + leader election |
| PostgreSQL 15 | Task history + audit logs |
| Prometheus | Metrics collection |
| Grafana | Dashboards |
| Elasticsearch | Log aggregation |

---

## 🗺️ 8-Week Roadmap

| Week | Milestone |
|---|---|
| **Week 1–2** | ✅ React UI + API Gateway foundation |
| **Week 3–4** | 🔨 Kubernetes Operator (CRD + Controller) — current |
| **Week 5–6** | 🔄 Scheduler algorithms + HPA/KEDA autoscaler |
| **Week 7–8** | 🔄 Storage layer + full observability stack |

---

## 📦 Deliverables

When complete this project will include:

- ✅ **React Dashboard** — task submission, Gantt chart, live status
- 🔨 **Custom K8s Operator** — TaskJob CRD with full lifecycle management
- 🔄 **Scheduler Engine** — MLFQ, SJF, Round Robin, FIFO algorithms
- 🔄 **Grafana Dashboard** — real-time scheduler metrics
- 🔄 **Helm Chart** — one-command cluster deployment
- 🔄 **OS Concepts Report** — every concept mapped to code

---

## 👩‍💻 Author

**Purva** **Sejal**
Built as a learning project covering Kubernetes, Cloud Task Scheduling, and Operating System concepts end-to-end.

---

## 📄 License

MIT License — free to use, modify, and distribute.
