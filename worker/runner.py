import os
import sys
import time
import json
import redis
import subprocess
import traceback

REDIS_URL = os.environ.get("REDIS_URL", "redis://redis-svc:6379")
TASK_QUEUE = "cloudos:task-queue"
LOG_PREFIX = "cloudos:logs"
STATUS_PREFIX = "cloudos:status"

def get_redis():
    r = redis.from_url(REDIS_URL)
    r.ping()
    return r

def log(r, task_id, message):
    entry = json.dumps({
        "task_id": task_id,
        "timestamp": time.time(),
        "message": message
    })
    r.rpush(f"{LOG_PREFIX}:{task_id}", entry)
    r.publish(f"cloudos:events", entry)
    print(f"[{task_id}] {message}", flush=True)

def set_status(r, task_id, status, exit_code=None):
    data = {
        "task_id": task_id,
        "status": status,
        "timestamp": time.time()
    }
    if exit_code is not None:
        data["exit_code"] = exit_code
    r.set(f"{STATUS_PREFIX}:{task_id}", json.dumps(data))
    r.publish("cloudos:events", json.dumps(data))

def run_task(r, task_id, command):
    log(r, task_id, f"Starting task: {command}")
    set_status(r, task_id, "running")

    try:
        process = subprocess.Popen(
            command,
            shell=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True
        )

        for line in process.stdout:
            log(r, task_id, line.strip())

        process.wait()
        exit_code = process.returncode

        if exit_code == 0:
            log(r, task_id, f"Task completed successfully")
            set_status(r, task_id, "completed", exit_code=0)
        else:
            log(r, task_id, f"Task failed with exit code {exit_code}")
            set_status(r, task_id, "failed", exit_code=exit_code)

    except Exception as e:
        log(r, task_id, f"Exception: {traceback.format_exc()}")
        set_status(r, task_id, "failed", exit_code=1)

def main():
    print("CloudOS Worker starting...", flush=True)
    r = get_redis()
    print("Connected to Redis!", flush=True)

    task_id = os.environ.get("CLOUDOS_TASK_ID", "unknown")
    command = os.environ.get("CLOUDOS_COMMAND", "echo 'No command specified'")

    run_task(r, task_id, command)
    print("Worker done.", flush=True)

if __name__ == "__main__":
    main()
