import json
import time
import redis
import psycopg2
from typing import List, Optional, Dict
from dataclasses import dataclass

@dataclass
class TaskRecord:
    task_id:    str
    command:    str
    priority:   str
    algorithm:  str
    status:     str
    node_id:    str
    created_at: float
    updated_at: float
    logs:       List[str] = None

class RedisStreamStore:
    """
    Redis Streams for real-time task event streaming.
    Mimics OS IPC — processes communicate via message streams.
    """
    def __init__(self, redis_url: str = "redis://redis-svc:6379"):
        self.r = redis.from_url(redis_url)
        self.stream_key = "cloudos:task-events"
        self.log_prefix = "cloudos:logs"

    def publish_event(self, task_id: str, event: str, data: Dict):
        self.r.xadd(self.stream_key, {
            "task_id":   task_id,
            "event":     event,
            "data":      json.dumps(data),
            "timestamp": str(time.time())
        })
        print(f"[STREAM] Published {event} for {task_id}")

    def push_log(self, task_id: str, message: str):
        log_entry = json.dumps({
            "task_id":   task_id,
            "message":   message,
            "timestamp": time.time()
        })
        self.r.rpush(f"{self.log_prefix}:{task_id}", log_entry)
        self.r.publish("cloudos:events", log_entry)

    def get_logs(self, task_id: str) -> List[str]:
        logs = self.r.lrange(f"{self.log_prefix}:{task_id}", 0, -1)
        return [json.loads(l) for l in logs]

    def get_events(self, count: int = 10) -> List[Dict]:
        events = self.r.xrevrange(self.stream_key, count=count)
        return [{"id": e[0], **{k.decode(): v.decode() for k, v in e[1].items()}}
                for e in events]

    def set_status(self, task_id: str, status: str):
        self.r.set(f"cloudos:status:{task_id}", json.dumps({
            "task_id":   task_id,
            "status":    status,
            "timestamp": time.time()
        }))

    def get_status(self, task_id: str) -> Optional[Dict]:
        val = self.r.get(f"cloudos:status:{task_id}")
        return json.loads(val) if val else None

    def queue_depth(self) -> int:
        return self.r.llen("cloudos:task-queue")

    def stats(self) -> Dict:
        return {
            "queue_depth":   self.queue_depth(),
            "stream_length": self.r.xlen(self.stream_key),
            "connected":     True
        }


class PostgresStore:
    """
    PostgreSQL for persistent task history and audit logs.
    Mimics OS process accounting — tracks all task executions.
    """
    def __init__(self):
        self.conn_str = (
            "host=postgres-svc port=5432 "
            "dbname=cloudos user=cloudos password=cloudos123"
        )

    def connect(self):
        return psycopg2.connect(self.conn_str)

    def init_tables(self):
        with self.connect() as conn:
            with conn.cursor() as cur:
                cur.execute("""
                    CREATE TABLE IF NOT EXISTS tasks (
                        task_id     VARCHAR(255) PRIMARY KEY,
                        command     TEXT,
                        priority    VARCHAR(50),
                        algorithm   VARCHAR(50),
                        status      VARCHAR(50),
                        node_id     VARCHAR(100),
                        created_at  FLOAT,
                        updated_at  FLOAT
                    )
                """)
                cur.execute("""
                    CREATE TABLE IF NOT EXISTS task_logs (
                        id          SERIAL PRIMARY KEY,
                        task_id     VARCHAR(255),
                        message     TEXT,
                        timestamp   FLOAT,
                        FOREIGN KEY (task_id) REFERENCES tasks(task_id)
                    )
                """)
            conn.commit()
        print("[POSTGRES] Tables initialized")

    def save_task(self, task: TaskRecord):
        with self.connect() as conn:
            with conn.cursor() as cur:
                cur.execute("""
                    INSERT INTO tasks
                        (task_id, command, priority, algorithm,
                         status, node_id, created_at, updated_at)
                    VALUES (%s,%s,%s,%s,%s,%s,%s,%s)
                    ON CONFLICT (task_id) DO UPDATE SET
                        status=EXCLUDED.status,
                        updated_at=EXCLUDED.updated_at
                """, (
                    task.task_id, task.command, task.priority,
                    task.algorithm, task.status, task.node_id,
                    task.created_at, task.updated_at
                ))
            conn.commit()
        print(f"[POSTGRES] Saved task {task.task_id}")

    def get_task(self, task_id: str) -> Optional[Dict]:
        with self.connect() as conn:
            with conn.cursor() as cur:
                cur.execute("SELECT * FROM tasks WHERE task_id=%s", (task_id,))
                row = cur.fetchone()
                if not row:
                    return None
                cols = [d[0] for d in cur.description]
                return dict(zip(cols, row))

    def get_all_tasks(self) -> List[Dict]:
        with self.connect() as conn:
            with conn.cursor() as cur:
                cur.execute("SELECT * FROM tasks ORDER BY created_at DESC")
                rows = cur.fetchall()
                cols = [d[0] for d in cur.description]
                return [dict(zip(cols, r)) for r in rows]

    def update_status(self, task_id: str, status: str):
        with self.connect() as conn:
            with conn.cursor() as cur:
                cur.execute("""
                    UPDATE tasks SET status=%s, updated_at=%s
                    WHERE task_id=%s
                """, (status, time.time(), task_id))
            conn.commit()
