import time
import heapq
from dataclasses import dataclass, field
from typing import List, Optional
from enum import IntEnum

class Priority(IntEnum):
    CRITICAL = 0
    HIGH     = 1
    MEDIUM   = 2
    LOW      = 3

@dataclass(order=True)
class Task:
    priority:           int
    submitted_at:       float
    task_id:            str   = field(compare=False)
    command:            str   = field(compare=False)
    cpu_request:        float = field(compare=False, default=0.5)
    memory_request:     float = field(compare=False, default=256.0)
    estimated_duration: int   = field(compare=False, default=30)
    burst_remaining:    int   = field(compare=False, default=30)

class MLFQueue:
    LEVELS   = 4
    QUANTUMS = [2, 4, 8, 16]

    def __init__(self):
        self.queues: List[List[Task]] = [[] for _ in range(self.LEVELS)]

    def enqueue(self, task: Task):
        level = int(task.priority)
        heapq.heappush(self.queues[level], task)
        print(f"[MLFQ] Enqueued {task.task_id} at level {level}")

    def dequeue(self) -> Optional[Task]:
        for level, queue in enumerate(self.queues):
            if queue:
                task = heapq.heappop(queue)
                print(f"[MLFQ] Dequeued {task.task_id} from level {level}")
                return task
        return None

    def demote(self, task: Task):
        new_level = min(int(task.priority) + 1, self.LEVELS - 1)
        task.priority = new_level
        heapq.heappush(self.queues[new_level], task)
        print(f"[MLFQ] Demoted {task.task_id} to level {new_level}")

    def stats(self):
        return {f"level_{i}": len(q) for i, q in enumerate(self.queues)}


class RoundRobinScheduler:
    QUANTUM = 5

    def __init__(self):
        self.queue: List[Task] = []
        self.index = 0

    def enqueue(self, task: Task):
        self.queue.append(task)

    def next_task(self) -> Optional[Task]:
        if not self.queue:
            return None
        task = self.queue[self.index % len(self.queue)]
        self.index += 1
        return task

    def complete(self, task: Task):
        if task in self.queue:
            self.queue.remove(task)


class SJFScheduler:
    def __init__(self):
        self.queue: List[Task] = []

    def enqueue(self, task: Task):
        self.queue.append(task)
        self.queue.sort(key=lambda t: t.estimated_duration)

    def dequeue(self) -> Optional[Task]:
        return self.queue.pop(0) if self.queue else None


class FIFOScheduler:
    def __init__(self):
        self.queue: List[Task] = []

    def enqueue(self, task: Task):
        self.queue.append(task)

    def dequeue(self) -> Optional[Task]:
        return self.queue.pop(0) if self.queue else None


def get_scheduler(algorithm: str):
    return {
        "mlfq": MLFQueue,
        "rr":   RoundRobinScheduler,
        "sjf":  SJFScheduler,
        "fifo": FIFOScheduler,
    }.get(algorithm, MLFQueue)()
