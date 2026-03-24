import time
import redis
import json
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
from typing import List, Optional
from priority_queue import get_scheduler, Task, Priority
from deadlock_detector import DeadlockDetector
from resource_allocator import ResourceAllocator

app = FastAPI(
    title="CloudOS Scheduler API",
    description="OS-inspired distributed task scheduler",
    version="1.0.0"
)

# Global state
scheduler     = get_scheduler("mlfq")
detector      = DeadlockDetector()
allocator     = ResourceAllocator()
redis_client  = None

# Add default nodes to allocator
allocator.add_node("node-1", cpu=4.0, memory=4096)
allocator.add_node("node-2", cpu=4.0, memory=4096)
allocator.add_node("node-3", cpu=8.0, memory=8192)

def get_redis():
    global redis_client
    if redis_client is None:
        try:
            redis_client = redis.from_url("redis://redis-svc:6379")
            redis_client.ping()
        except Exception:
            redis_client = None
    return redis_client

# --- Request Models ---

class SubmitTaskRequest(BaseModel):
    task_id:            str
    command:            str
    priority:           str = "medium"
    algorithm:          str = "mlfq"
    cpu_request:        float = 0.5
    memory_request:     float = 256.0
    estimated_duration: int = 30
    depends_on:         List[str] = []

class AlgorithmRequest(BaseModel):
    algorithm: str

# --- Endpoints ---

@app.get("/health")
def health():
    return {"status": "ok", "service": "cloudos-scheduler"}

@app.post("/tasks/submit")
def submit_task(req: SubmitTaskRequest):
    # 1. Map priority string to int
    priority_map = {
        "critical": Priority.CRITICAL,
        "high":     Priority.HIGH,
        "medium":   Priority.MEDIUM,
        "low":      Priority.LOW,
    }
    priority = priority_map.get(req.priority.lower(), Priority.MEDIUM)

    # 2. Register dependencies and check deadlock
    detector.add_task(req.task_id, req.depends_on)
    if detector.has_deadlock():
        detector.remove_task(req.task_id)
        cycle = detector.get_cycle()
        raise HTTPException(
            status_code=400,
            detail=f"Deadlock detected! Cycle: {cycle}"
        )

    # 3. Allocate resources
    result = allocator.allocate(
        req.task_id,
        req.cpu_request,
        req.memory_request
    )
    if not result.success:
        raise HTTPException(status_code=507, detail=result.message)

    # 4. Create task and enqueue
    task = Task(
        priority=int(priority),
        submitted_at=time.time(),
        task_id=req.task_id,
        command=req.command,
        cpu_request=req.cpu_request,
        memory_request=req.memory_request,
        estimated_duration=req.estimated_duration,
    )

    sched = get_scheduler(req.algorithm)
    sched.enqueue(task)

    # 5. Push to Redis queue
    r = get_redis()
    if r:
        r.lpush("cloudos:task-queue", json.dumps({
            "task_id":   req.task_id,
            "command":   req.command,
            "priority":  req.priority,
            "algorithm": req.algorithm,
            "node":      result.node_id,
        }))

    return {
        "status":    "queued",
        "task_id":   req.task_id,
        "node":      result.node_id,
        "priority":  req.priority,
        "algorithm": req.algorithm,
    }

@app.get("/tasks/next")
def get_next_task():
    task = scheduler.dequeue()
    if not task:
        raise HTTPException(status_code=404, detail="No tasks in queue")
    return {
        "task_id":           task.task_id,
        "command":           task.command,
        "priority":          task.priority,
        "estimated_duration":task.estimated_duration,
    }

@app.get("/scheduler/stats")
def scheduler_stats():
    return {
        "queue_stats":   scheduler.stats() if hasattr(scheduler, "stats") else {},
        "cluster_stats": allocator.cluster_stats(),
        "deadlock":      detector.visualize(),
    }

@app.get("/scheduler/execution-order")
def execution_order():
    order = detector.get_execution_order()
    if order is None:
        raise HTTPException(status_code=400, detail="Deadlock detected!")
    return {"execution_order": order}

@app.post("/scheduler/algorithm")
def change_algorithm(req: AlgorithmRequest):
    global scheduler
    scheduler = get_scheduler(req.algorithm)
    return {"status": "ok", "algorithm": req.algorithm}

@app.get("/cluster/stats")
def cluster_stats():
    return allocator.cluster_stats()

if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=8000)
