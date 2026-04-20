# -*- coding: utf-8 -*-
"""
main.py -- AntiGravity Scheduler: FastAPI HTTP Gateway
=======================================================

OS Analogy: Kernel Syscall Interface
--------------------------------------
The API gateway is the distributed system's equivalent of the kernel syscall layer:

  - Every user-space program (client) communicates with the kernel (scheduler brain)
    ONLY through defined syscall entry points -- not by touching kernel data directly.
  - Similarly, every external client (Postman, frontend, CLI) communicates with the
    in-memory Scheduler, DeadlockDetector, and ResourceAllocator ONLY through
    these HTTP routes -- never by importing those modules directly.

Architecture layers (top to bottom):
  HTTP Client  <-->  FastAPI Gateway  <-->  Scheduler Core (Phase 2)
  (user space)       (syscall layer)        (kernel internals)

This mirrors the Linux VFS (Virtual File System) layer that provides a uniform
read()/write()/open() interface regardless of the underlying filesystem (ext4, btrfs, tmpfs).
Our routes provide a uniform REST interface regardless of which scheduling algorithm
handles the task underneath.

Run with:
    cd gateway/
    uvicorn main:app --reload --port 8000
"""

from __future__ import annotations

import sys
import os

# -- Force UTF-8 output on Windows for Unicode checkmarks/emojis --
if hasattr(sys.stdout, 'reconfigure'):
    sys.stdout.reconfigure(encoding='utf-8', errors='replace')

import uuid
import time
from contextlib import asynccontextmanager
from datetime import timedelta
from typing import Dict, List, Optional, Any

# -- Ensure scheduler/ (Phase 2) is importable from gateway/ sibling folder --
_SCHEDULER_DIR = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "scheduler"))
if _SCHEDULER_DIR not in sys.path:
    sys.path.insert(0, _SCHEDULER_DIR)

from fastapi import (
    Depends, FastAPI, HTTPException, Request, Response, status
)
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse
from fastapi.security import OAuth2PasswordRequestForm
from pydantic import BaseModel, Field

# -- Phase 2 imports --
from models import Task, Priority, TaskStatus, Algorithm
from priority_queue import Scheduler
from deadlock_detector import DeadlockDetector
from resource_allocator import ResourceAllocator, Worker

# -- Phase 3 auth / rate-limiting --
# Use absolute import path to avoid issues regardless of working directory
sys.path.insert(0, os.path.dirname(__file__))
from auth import (
    Token, UserInDB, authenticate_user, create_access_token,
    get_current_user, require_role, ACCESS_TOKEN_EXPIRE_MINUTES,
)
from rate_limit import rate_limit_dependency, get_rate_limiter, ROLE_LIMITS


# ===========================================================================
# In-memory state (the "kernel data structures" of our system)
# ===========================================================================

# Global scheduler instances -- live for the entire process lifetime.
# Analogous to kernel global data structures: runqueue, mm_struct, etc.
_scheduler   = Scheduler()
_detector    = DeadlockDetector()
_allocator   = ResourceAllocator(strategy="best_fit")

# Master task registry: task_id -> Task object
# FastAPI routes look up tasks here instead of digging through scheduler internals.
_task_store: Dict[str, Task] = {}

# Map of task_id -> routing Algorithm chosen at submission time.
# Required so we can requeue tasks correctly if allocation fails.
_task_algos: Dict[str, Algorithm] = {}

# Set of task IDs that have been submitted to the scheduler queues.
# Tasks with dependencies start PENDING but not submitted.
_submitted_tasks: set[str] = set()


def _trigger_scheduling():
    """
    Reactive scheduling trigger: 
      1. Promotes tasks from WAITING to READY if dependencies are met.
      2. Attempts to dispatch READY tasks to workers until resources are full.
    """
    print("[Trigger] Running scheduling pass...")
    
    # --- Part 1: Dependency Promotion ---
    all_ids = list(_task_store.keys())
    completed_ids = {tid for tid, t in _task_store.items() if t.status == TaskStatus.COMPLETED}
    ready_ids = _detector.get_ready_tasks(all_ids, completed_ids)
    
    promoted = 0
    for rid in ready_ids:
        if rid not in _submitted_tasks:
            task = _task_store[rid]
            # Tasks can be rejected by quota; don't promote them.
            if task.status == TaskStatus.REJECTED:
                continue
                
            algo = _scheduler.submit(task)
            _task_algos[rid] = algo
            _submitted_tasks.add(rid)
            promoted += 1
            print(f"[Trigger] Dependency unblocked: '{task.name}' ({rid}) → {algo.name}")

    # --- Part 2: Resource Allocation ---
    dispatched = 0
    while True:
        task = _scheduler.next_task()
        if not task:
            break
            
        worker = _allocator.allocate(task)
        if worker:
            print(f"[Trigger] Dispatched '{task.name}' ({task.id}) → {worker.id}")
            dispatched += 1
        else:
            # Requeue back to its specific algorithm queue
            algo = _task_algos.get(task.id, Algorithm.MLFQ)
            _scheduler.requeue(task, algo)
            print(f"[Trigger] Out of resources. Requeued '{task.name}'")
            break 
            
    print(f"[Trigger] Pass complete. Promoted {promoted}, Dispatched {dispatched}.")


# ===========================================================================
# Lifespan (startup / shutdown)
# ===========================================================================

@asynccontextmanager
async def lifespan(app: FastAPI):
    """
    FastAPI lifespan handler -- runs startup code before serving, cleanup on exit.

    OS Analogy: The kernel's start_kernel() during boot:
      1. Initialise core subsystems (memory, scheduler, VFS)
      2. Register devices (register_worker = plugging in CPUs)
      3. Open the system to user-space (start serving HTTP = init PID 1)
    """
    # --- STARTUP ---
    print("\n" + "="*65)
    print("  AntiGravity Scheduler API -- Online")
    print("  Phase 3: FastAPI Gateway  |  Phase 2: Scheduler Core")
    print("="*65)

    # Register fake workers (analogous to CPU topology discovery during boot)
    workers = [
        Worker(id="worker-1", total_cpu=4.0, total_mem=8192),
        Worker(id="worker-2", total_cpu=4.0, total_mem=8192),
        Worker(id="worker-3", total_cpu=2.0, total_mem=4096),
    ]
    for w in workers:
        _allocator.register_worker(w)
    print(f"  Workers registered: {[w.id for w in workers]}")
    print("="*65 + "\n")

    yield  # Application runs here

    # --- SHUTDOWN ---
    print("\n  Shutting down AntiGravity Scheduler API...")
    running = [t for t in _task_store.values() if t.status == TaskStatus.RUNNING]
    if running:
        print(f"  WARNING: {len(running)} tasks still RUNNING at shutdown: "
              f"{[t.name for t in running]}")
    print("  Goodbye.\n")


# ===========================================================================
# FastAPI app
# ===========================================================================

app = FastAPI(
    title="AntiGravity Scheduler API",
    description=(
        "Phase 3 HTTP gateway for the AntiGravity distributed task scheduler. "
        "Wraps the Phase 2 scheduling core (RR/SJF/MLFQ + deadlock detection + "
        "resource allocation) behind a REST interface with JWT auth and rate limiting."
    ),
    version="1.0.0",
    lifespan=lifespan,
)


# ---------------------------------------------------------------------------
# CORS Middleware (allow all origins for Postman / local frontend testing)
# ---------------------------------------------------------------------------
app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],       # Restrict to specific domains in production
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)


# ---------------------------------------------------------------------------
# Request Logging Middleware
# ---------------------------------------------------------------------------
@app.middleware("http")
async def logging_middleware(request: Request, call_next):
    """
    Log every HTTP request and response.

    OS Analogy: Linux's audit subsystem (auditd) -- every syscall can be logged
    with its caller, arguments, and return code for security auditing.
    Here we log METHOD + path + status code for every API call.
    """
    start = time.monotonic()
    request_id = str(uuid.uuid4())[:8]
    request.state.request_id = request_id

    response = await call_next(request)

    elapsed_ms = (time.monotonic() - start) * 1000

    # Propagate rate limit headers if set by dependency
    if hasattr(request.state, "rate_limit_remaining"):
        response.headers["X-RateLimit-Limit"] = str(getattr(request.state, "rate_limit_limit", ""))
        response.headers["X-RateLimit-Remaining"] = str(request.state.rate_limit_remaining)
        response.headers["X-RateLimit-Reset"] = str(request.state.rate_limit_reset)

    print(f"[HTTP] [{request_id}] {request.method} {request.url.path} "
          f"-> {response.status_code} ({elapsed_ms:.1f}ms)")
    return response


# ---------------------------------------------------------------------------
# Global Exception Handler
# ---------------------------------------------------------------------------
@app.exception_handler(Exception)
async def global_exception_handler(request: Request, exc: Exception):
    """
    Catch-all for unhandled exceptions -- return a structured 500 error.

    Request IDs allow correlating client errors with server logs --
    analogous to Linux's OOPS dump with a stack trace when the kernel faults.
    """
    request_id = getattr(request.state, "request_id", str(uuid.uuid4())[:8])
    print(f"[ERROR] [{request_id}] Unhandled exception on "
          f"{request.method} {request.url.path}: {exc}")
    return JSONResponse(
        status_code=500,
        content={
            "error": "internal_server_error",
            "detail": str(exc),
            "request_id": request_id,
        },
    )


# ---------------------------------------------------------------------------
# HTTPException handler (adds request_id to all 4xx/5xx responses)
# ---------------------------------------------------------------------------
@app.exception_handler(HTTPException)
async def http_exception_handler(request: Request, exc: HTTPException):
    """
    Intercept HTTPException raised by our routes and append a request_id for
    tracing. FastAPI's default handler would not include the UUID we generated
    in the logging middleware.
    """
    request_id = getattr(request.state, "request_id", str(uuid.uuid4())[:8])
    # exc.detail may be a str or a dict; normalise to dict so we can insert
    if isinstance(exc.detail, dict):
        content = exc.detail.copy()
    else:
        content = {"detail": exc.detail}
    content["request_id"] = request_id
    return JSONResponse(status_code=exc.status_code, content=content, headers=exc.headers)


# ===========================================================================
# Pydantic Request / Response Models
# (analogous to kernel IOCTL struct definitions -- typed data contracts)
# ===========================================================================

class TaskRequest(BaseModel):
    """Request body for submitting a new task.

    ``id`` is optional and only used for testing/debugging. In normal operation the
    server generates a short UUID for each task. Allowing the client to supply an
    ``id`` makes it possible to craft deterministic dependency graphs (e.g. to
    test the deadlock detector). This mimics how some RPC systems allow a caller
    to provide a request ID for idempotency; here it's purely a test helper.
    """
    id: Optional[str] = Field(
        None,
        description="Optional client-supplied task identifier (8‑char string).",
    )
    name:         str
    burst_time:   float = Field(..., gt=0, description="Estimated CPU seconds")
    priority:     str   = Field("NORMAL", description="LOW|NORMAL|HIGH|CRITICAL")
    dependencies: List[str] = Field(default_factory=list)
    cpu_cores:    float = Field(1.0, gt=0, le=8.0)
    memory_mb:    int   = Field(256, gt=0, le=32768)


class TaskResponse(BaseModel):
    """Response body for a single task."""
    id:          str
    name:        str
    status:      str
    priority:    str
    burst_time:  float
    cpu_cores:   float
    memory_mb:   int
    worker_id:   Optional[str]
    queue_level: int
    wait_time:   float
    dependencies: List[str]
    message:     str = ""


class WorkerInfo(BaseModel):
    """Per-worker state for cluster response."""
    id:            str
    total_cpu:     float
    total_mem:     int
    cpu_util_pct:  float
    mem_util_pct:  float
    free_cpu:      float
    free_mem:      int
    running_tasks: List[str]
    is_idle:       bool


class ClusterStateResponse(BaseModel):
    """Response body for GET /cluster."""
    workers:        List[WorkerInfo]
    total_cpu:      float
    used_cpu:       float
    total_mem_mb:   int
    used_mem_mb:    int
    cpu_util_pct:   float
    mem_util_pct:   float


class MetricsResponse(BaseModel):
    """Response body for GET /metrics."""
    total_completed:      int
    total_pending:        int
    total_running:        int
    avg_wait_time:        float
    avg_turnaround_time:  float
    algo_distribution:    Dict[str, int]


class GraphResponse(BaseModel):
    """Response body for GET /graph."""
    graph:              Dict[str, List[str]]
    topological_order:  Optional[List[str]]
    ready_tasks:        List[str]
    node_count:         int
    edge_count:         int


class HealthResponse(BaseModel):
    """Response body for GET /health."""
    status:    str
    timestamp: float
    scheduler: str
    workers:   int
    tasks:     int


# ===========================================================================
# Helper utilities
# ===========================================================================

def _task_to_response(task: Task, message: str = "") -> TaskResponse:
    """Convert a Task object to a TaskResponse Pydantic model."""
    return TaskResponse(
        id=task.id,
        name=task.name,
        status=task.status.name,
        priority=task.priority.name,
        burst_time=task.burst_time,
        cpu_cores=task.cpu_cores,
        memory_mb=task.memory_mb,
        worker_id=task.worker_id,
        queue_level=task.queue_level,
        wait_time=round(task.wait_time, 4),
        dependencies=task.dependencies,
        message=message,
    )


def _get_task_or_404(task_id: str) -> Task:
    """Look up a task by ID or raise HTTP 404."""
    task = _task_store.get(task_id)
    if task is None:
        raise HTTPException(status_code=404, detail=f"Task '{task_id}' not found")
    return task


# ===========================================================================
# Routes
# ===========================================================================

# ---------------------------------------------------------------------------
# Auth
# ---------------------------------------------------------------------------

@app.post("/auth/token", response_model=Token, tags=["Auth"])
async def login(form_data: OAuth2PasswordRequestForm = Depends()):
    """
    Login endpoint: exchange username+password for a JWT access token.

    Accepts OAuth2 form data (application/x-www-form-urlencoded):
      username=alice&password=alicepass

    Returns a Bearer JWT token valid for ACCESS_TOKEN_EXPIRE_MINUTES minutes.

    OS Analogy: The login(1) program -- verifies credentials via PAM and
    creates a new login session (here: a signed JWT instead of a TTY session).
    """
    user = authenticate_user(form_data.username, form_data.password)
    if not user:
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail="Incorrect username or password",
            headers={"WWW-Authenticate": "Bearer"},
        )
    access_token = create_access_token(
        data={"sub": user.username, "role": user.role},
        expires_delta=timedelta(minutes=ACCESS_TOKEN_EXPIRE_MINUTES),
    )
    return Token(access_token=access_token, token_type="bearer")


# ---------------------------------------------------------------------------
# Health
# ---------------------------------------------------------------------------

@app.get("/health", response_model=HealthResponse, tags=["System"])
async def health_check():
    """
    Health check endpoint -- no authentication required.

    Returns the API online status, timestamp, and quick stats.
    Used by load balancers and monitoring systems to verify the service is alive.

    OS Analogy: /proc/sys/kernel/hostname + uptime(1) -- a quick ping to confirm
    the system is responsive without requiring privileged access.
    """
    return HealthResponse(
        status="ok",
        timestamp=time.time(),
        scheduler="AntiGravity v1.0",
        workers=len(_allocator.workers),
        tasks=len(_task_store),
    )


# ---------------------------------------------------------------------------
# Tasks -- CRUD
# ---------------------------------------------------------------------------

@app.post(
    "/tasks",
    response_model=TaskResponse,
    status_code=201,
    tags=["Tasks"],
    dependencies=[Depends(require_role("admin", "user"))],
)
async def submit_task(
    body: TaskRequest,
    request: Request,
    current_user: UserInDB = Depends(rate_limit_dependency),
):
    """
    Submit a new task to the scheduler.

    Pipeline:
      1. Parse and validate the TaskRequest.
      2. Check resource quota (ulimit analog) -- reject if over limits.
      3. Deadlock dry-run: can_add() -- reject 409 if would create a cycle.
      4. Register in detector graph.
      5. Submit to the master Scheduler (auto-routes to RR/SJF/MLFQ).
      6. Optimistically allocate a worker (task stays PENDING if none available).

    OS Analogy: fork(2) + execve(2) -- creates a new process entry (task),
    validates resource limits (RLIMIT_*), and places it in the run queue.
    """
    request_id = getattr(request.state, "request_id", "?")

    # Resolve priority enum -- default to NORMAL for unknown strings
    try:
        priority_enum = Priority[body.priority.upper()]
    except KeyError:
        raise HTTPException(
            status_code=422,
            detail={
                "error": "invalid_priority",
                "message": f"Invalid priority '{body.priority}'. "
                           f"Choose from: {[p.name for p in Priority]}"
            },
        )

    # Allow an optional client-supplied id for testing/deadlock scenarios.
    # If supplied, ensure it doesn't collide with an existing task.
    if body.id:
        task_id = body.id
        if task_id in _task_store:
            raise HTTPException(
                status_code=422,
                detail={
                    "error": "duplicate_id",
                    "message": f"Task id '{task_id}' already exists."
                },
            )
    else:
        task_id = str(uuid.uuid4())[:8]
    task = Task(
        id=task_id,
        name=body.name,
        burst_time=body.burst_time,
        priority=priority_enum,
        dependencies=body.dependencies,
        cpu_cores=body.cpu_cores,
        memory_mb=body.memory_mb,
        arrival_time=time.time(),
    )

    print(f"[TASK] [{request_id}] Submit request from '{current_user.username}': "
          f"'{body.name}' (burst={body.burst_time}s, priority={body.priority})")

    # Step 2: quota check
    if not _allocator.check_quota(task):
        task.status = TaskStatus.REJECTED
        _task_store[task_id] = task
        raise HTTPException(
            status_code=422,
            detail=f"Task '{body.name}' exceeds resource quota. "
                   f"Max CPU: 4.0 cores, Max MEM: 4096 MB."
        )

    # Verify that dependency IDs actually exist
    for dep_id in body.dependencies:
        if dep_id not in _task_store:
            raise HTTPException(
                status_code=422,
                detail=f"Dependency task_id '{dep_id}' does not exist."
            )

    # Step 3: deadlock dry-run
    safe, cycle = _detector.can_add(task_id, body.dependencies)
    if not safe:
        raise HTTPException(
            status_code=409,
            detail={
                "error": "deadlock_detected",
                "message": "Adding this task would create a circular dependency (deadlock).",
                "cycle": cycle,
                "request_id": request_id,
            }
        )

    # Step 4: register in detector
    _detector.add_task(task_id, body.dependencies)

    # Step 5: Registry entry
    _task_store[task_id] = task

    # Step 6: Trigger the global scheduling loop
    # This will:
    #   a) Promote to Scheduler queues if no deps
    #   b) Try to allocate to a Worker if resources allow
    _trigger_scheduling()
    
    # Check current status for the response message
    if task.status == TaskStatus.RUNNING:
        msg = f"Task submitted and allocated to {task.worker_id}"
    elif task.dependencies:
        msg = "Task submitted. Waiting for dependencies to complete."
    else:
        msg = "Task submitted. Waiting for available worker resources."

    print(f"[TASK] [{request_id}] Task '{task_id}' stored. Total tasks: {len(_task_store)}")
    return _task_to_response(task, message=msg)


@app.get("/tasks", response_model=List[TaskResponse], tags=["Tasks"])
async def list_tasks(
    request: Request,
    status_filter: Optional[str] = None,
    limit: int = 20,
    offset: int = 0,
    current_user: UserInDB = Depends(rate_limit_dependency),
):
    """
    List all submitted tasks with optional status filtering and pagination.

    Query params:
      ?status=pending|running|completed|failed|rejected  (case-insensitive)
      ?limit=20&offset=0

    OS Analogy: ps(1) / /proc listing -- enumerate all processes visible to
    the calling user. In a real multi-tenant system, non-admin users would only
    see their own tasks (like how processes are scoped to UIDs in Linux).
    """
    tasks = list(_task_store.values())

    if status_filter:
        try:
            filter_enum = TaskStatus[status_filter.upper()]
            tasks = [t for t in tasks if t.status == filter_enum]
        except KeyError:
            raise HTTPException(
                status_code=422,
                detail=f"Invalid status filter '{status_filter}'. "
                       f"Valid: {[s.name.lower() for s in TaskStatus]}"
            )

    total = len(tasks)
    tasks = tasks[offset: offset + limit]
    print(f"[TASK] list_tasks: user='{current_user.username}' "
          f"filter={status_filter} total={total} returning={len(tasks)}")
    return [_task_to_response(t) for t in tasks]


@app.get("/tasks/{task_id}", response_model=TaskResponse, tags=["Tasks"])
async def get_task(
    task_id: str,
    current_user: UserInDB = Depends(rate_limit_dependency),
):
    """
    Get full details for a single task by its ID.

    Returns 404 if the task ID does not exist in _task_store.

    OS Analogy: /proc/<pid>/status -- reading a single process's scheduling
    state, memory usage, and execution metadata.
    """
    task = _get_task_or_404(task_id)
    print(f"[TASK] get_task: '{task_id}' requested by '{current_user.username}'")
    return _task_to_response(task)


@app.delete("/tasks/{task_id}", tags=["Tasks"],
            dependencies=[Depends(require_role("admin"))])
async def cancel_task(
    task_id: str,
    request: Request,
    current_user: UserInDB = Depends(get_current_user),
):
    """
    Cancel a PENDING task. Admin only.

    Only PENDING tasks can be cancelled -- you cannot cancel an already-RUNNING
    task (analogous to how SIGKILL can terminate a sleeping process but a process
    in uninterruptible sleep (D state) cannot be killed until the I/O completes).

    Removes the task from the deadlock detector graph and marks it REJECTED.
    Returns 400 if task is already RUNNING or COMPLETED.

    OS Analogy: kill(2) with SIGTERM -- request graceful termination. For a
    task in RUNNING state, we'd need a worker-side interrupt (out of scope here).
    """
    request_id = getattr(request.state, "request_id", "?")
    task = _get_task_or_404(task_id)

    if task.status == TaskStatus.RUNNING:
        raise HTTPException(
            status_code=400,
            detail=f"Cannot cancel task '{task_id}' -- it is currently RUNNING. "
                   "Wait for it to complete or use POST /tasks/{id}/complete."
        )
    if task.status in (TaskStatus.COMPLETED, TaskStatus.FAILED):
        raise HTTPException(
            status_code=400,
            detail=f"Task '{task_id}' is already {task.status.name} -- cannot cancel."
        )

    task.status = TaskStatus.REJECTED
    _detector.remove_task(task_id)
    print(f"[TASK] [{request_id}] Task '{task_id}' cancelled by admin '{current_user.username}'")
    return {"message": f"Task '{task_id}' ({task.name}) cancelled successfully.",
            "task_id": task_id}


@app.post(
    "/tasks/{task_id}/complete",
    response_model=TaskResponse,
    tags=["Tasks"],
    dependencies=[Depends(require_role("admin", "user"))],
)
async def complete_task(
    task_id: str,
    request: Request,
    current_user: UserInDB = Depends(get_current_user),
):
    """
    Mark a task as COMPLETED and release its resources.

    Triggers the full completion pipeline:
      1. scheduler.complete_task()  -- record end_time + accounting metrics
      2. allocator.release()        -- free CPU + RAM on the worker
      3. detector.remove_task()     -- remove from dependency graph (unblocks dependents)

    OS Analogy: do_exit(2) -- the kernel's task termination path:
      sets task state to ZOMBIE, releases mm_struct (memory), closes file descriptors,
      sends SIGCHLD to parent, and finally calls schedule() to run a new process.
    """
    request_id = getattr(request.state, "request_id", "?")
    task = _get_task_or_404(task_id)

    if task.status == TaskStatus.COMPLETED:
        return _task_to_response(task, message="Task was already completed.")
    if task.status in (TaskStatus.REJECTED, TaskStatus.FAILED):
        raise HTTPException(
            status_code=400,
            detail=f"Task '{task_id}' is {task.status.name} -- cannot complete."
        )

    # Full completion pipeline
    _scheduler.complete_task(task)
    _allocator.release(task)
    _detector.remove_task(task_id)

    # Trigger rescheduling now that resources are free!
    _trigger_scheduling()

    print(f"[TASK] [{request_id}] Task '{task_id}' completed by '{current_user.username}' | "
          f"wait={task.wait_time:.3f}s turnaround={task.turnaround_time:.3f}s")
    return _task_to_response(task, message="Task completed successfully.")


# ---------------------------------------------------------------------------
# Cluster
# ---------------------------------------------------------------------------

@app.get("/cluster", response_model=ClusterStateResponse, tags=["Cluster"])
async def get_cluster_state(
    current_user: UserInDB = Depends(rate_limit_dependency),
):
    """
    Return current worker pool state: per-worker CPU/memory utilization.

    OS Analogy: `kubectl top nodes` or /proc/cpuinfo + /proc/meminfo across
    all nodes in a cluster. Shows real-time resource consumption per worker.
    """
    worker_infos = []
    for w in _allocator.workers.values():
        worker_infos.append(WorkerInfo(
            id=w.id,
            total_cpu=w.total_cpu,
            total_mem=w.total_mem,
            cpu_util_pct=w.cpu_utilization,
            mem_util_pct=w.mem_utilization,
            free_cpu=w.free_cpu,
            free_mem=w.free_mem,
            running_tasks=list(w.running_tasks),
            is_idle=w.is_idle,
        ))

    cap = _allocator.total_cluster_capacity()
    return ClusterStateResponse(
        workers=worker_infos,
        total_cpu=cap["total_cpu"],
        used_cpu=cap["used_cpu"],
        total_mem_mb=cap["total_mem_mb"],
        used_mem_mb=cap["used_mem_mb"],
        cpu_util_pct=cap["cpu_util_pct"],
        mem_util_pct=cap["mem_util_pct"],
    )


# ---------------------------------------------------------------------------
# Metrics
# ---------------------------------------------------------------------------

@app.get(
    "/metrics",
    response_model=MetricsResponse,
    tags=["Monitoring"],
    dependencies=[Depends(require_role("admin"))],
)
async def get_metrics(current_user: UserInDB = Depends(get_current_user)):
    """
    Return scheduler performance metrics. Admin only.

    Reports:
      - avg_wait_time:       avg time tasks spent in READY queue
      - avg_turnaround_time: avg time from arrival to completion
      - algo_distribution:   how many tasks went to RR / SJF / MLFQ

    OS Analogy: /proc/schedstat -- Linux exposes per-CPU scheduling statistics
    including wait time, run time, and timeslice information for analysis.
    """
    completed = _scheduler._completed_tasks
    n = len(completed)

    avg_wait = sum(t.wait_time for t in completed) / n if n else 0.0
    avg_ta = (
        sum(t.turnaround_time for t in completed if t.turnaround_time) / n
        if n else 0.0
    )

    # Count pending and running
    pending = sum(1 for t in _task_store.values() if t.status == TaskStatus.PENDING)
    running = sum(1 for t in _task_store.values() if t.status == TaskStatus.RUNNING)

    # Approximate algo distribution from burst_time routing rules
    algo_dist: Dict[str, int] = {"MLFQ": 0, "SJF": 0, "ROUND_ROBIN": 0}
    for t in _task_store.values():
        if t.priority == Priority.CRITICAL or (3.0 < t.burst_time < 10.0):
            algo_dist["MLFQ"] += 1
        elif t.burst_time <= 3.0:
            algo_dist["SJF"] += 1
        else:
            algo_dist["ROUND_ROBIN"] += 1

    return MetricsResponse(
        total_completed=n,
        total_pending=pending,
        total_running=running,
        avg_wait_time=round(avg_wait, 5),
        avg_turnaround_time=round(avg_ta, 5),
        algo_distribution=algo_dist,
    )


# ---------------------------------------------------------------------------
# Dependency Graph
# ---------------------------------------------------------------------------

@app.get("/graph", response_model=GraphResponse, tags=["Monitoring"])
async def get_dependency_graph(
    current_user: UserInDB = Depends(rate_limit_dependency),
):
    """
    Return the live dependency graph, topological order, and ready tasks.

    Useful for visualising which tasks are blocked on which, and which are
    immediately runnable (dependencies all satisfied).

    OS Analogy: /proc/<pid>/fdinfo or lsof -- inspects the live kernel state
    to show which processes are waiting on which file descriptors / locks.
    Here we expose the task dependency (Wait-For Graph) in JSON form.
    """
    graph = _detector.graph
    topo = _detector.topological_order()

    all_ids = list(graph.keys())
    completed_ids = {
        t.id for t in _task_store.values()
        if t.status == TaskStatus.COMPLETED
    }
    ready = _detector.get_ready_tasks(all_ids, completed_ids)

    edge_count = sum(len(deps) for deps in graph.values())

    return GraphResponse(
        graph=dict(graph),
        topological_order=topo,
        ready_tasks=ready,
        node_count=len(graph),
        edge_count=edge_count,
    )


# ---------------------------------------------------------------------------
# Rate Limit Info (bonus introspection endpoint)
# ---------------------------------------------------------------------------

@app.get("/rate-limits", tags=["Monitoring"])
async def get_rate_limits(current_user: UserInDB = Depends(get_current_user)):
    """
    Return the current user's rate limit config and remaining tokens.

    Gives clients a way to check how many requests they have left before
    being throttled -- analogous to ulimit -a showing current rlimits.
    """
    limiter = get_rate_limiter()
    bucket = limiter.get_bucket(current_user.username, current_user.role)
    return {
        "username":       current_user.username,
        "role":           current_user.role,
        "limit_per_min":  ROLE_LIMITS.get(current_user.role, 10),
        "remaining":      bucket.remaining,
        "reset_in_secs":  bucket.reset_in,
    }
