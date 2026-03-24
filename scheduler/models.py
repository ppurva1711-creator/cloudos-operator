"""
models.py — AntiGravity Scheduler: Core Data Models
=====================================================

OS Analogy: Process Control Block (PCB)
----------------------------------------
In a real OS, every process is represented by a PCB — a kernel data structure that
stores everything the OS needs to schedule, suspend, and resume a process:
  PID, state (READY/RUNNING/BLOCKED), burst-time estimate, priority, register snapshot,
  memory maps, open file table, and accounting info (arrival, start, end times).

This module's `Task` dataclass is the scheduler-equivalent of a PCB for distributed
workloads. Instead of CPU registers we track resource requirements (cpu_cores, memory_mb)
and instead of a single OS queue-level we expose `queue_level` for MLFQ placement.

The enums mirror kernel concepts:
  - Priority  → nice values / real-time scheduling classes (SCHED_FIFO, SCHED_RR, CFS weight)
  - TaskStatus → process states in the 5-state model (new → ready → running → terminated/blocked)
  - Algorithm  → scheduling policy applied to this task's queue (analogous to Linux sched_class)
"""

from __future__ import annotations
from dataclasses import dataclass, field
from enum import Enum, auto
from typing import List, Optional
import time


# ---------------------------------------------------------------------------
# Enums
# ---------------------------------------------------------------------------

class Priority(Enum):
    """
    Maps to OS scheduling priority / nice values.
    Linux nice range: -20 (highest) to +19 (lowest).
    Here we use four symbolic tiers that the Scheduler maps to concrete queues.
    """
    LOW      = 1   # Nice +10  → background, best-effort
    NORMAL   = 2   # Nice  0   → default interactive processes
    HIGH     = 3   # Nice -10  → latency-sensitive services
    CRITICAL = 4   # Nice -20  → real-time / safety-critical tasks


class TaskStatus(Enum):
    """
    The classic 5-state process model used in every OS textbook:
      NEW → READY (PENDING) → RUNNING → TERMINATED (COMPLETED/FAILED)
    REJECTED is added for resource quota violations (ulimit analogy).
    """
    PENDING   = auto()   # In a scheduler queue, waiting for CPU (READY state)
    RUNNING   = auto()   # Currently executing on a worker (RUNNING state)
    COMPLETED = auto()   # Finished successfully (TERMINATED state)
    FAILED    = auto()   # Terminated with non-zero exit / exception
    REJECTED  = auto()   # Killed before execution: quota violation or unresolvable deps


class Algorithm(Enum):
    """
    Scheduling policy enum — analogous to Linux's sched_class pointer in task_struct.
    The master Scheduler selects the appropriate algorithm per task at submit time.
    """
    ROUND_ROBIN = auto()   # Time-sharing: fair CPU allocation via fixed quantum
    SJF         = auto()   # Shortest Job First: minimises avg. waiting time (provably optimal)
    MLFQ        = auto()   # Multi-Level Feedback Queue: adaptive, no prior burst knowledge needed


# ---------------------------------------------------------------------------
# Task (Process Control Block equivalent)
# ---------------------------------------------------------------------------

@dataclass
class Task:
    """
    Equivalent to Linux's `task_struct` / Windows' EPROCESS / KPROCESS block.

    Key fields and their OS parallels:
      id           → PID (Process Identifier)
      name         → comm[16] in task_struct (human-readable name)
      burst_time   → CPU burst estimate (used by SJF; in real OS approximated via exponential averaging)
      priority     → sched_priority / static_prio
      dependencies → list of PIDs this process is blocked on (like wait() / futex dependencies)
      cpu_cores    → CPU affinity / number of hardware threads requested
      memory_mb    → RSS / VSZ memory requirement
      status       → process state (ready/running/zombie/…)
      queue_level  → current MLFQ queue depth (0 = highest priority queue)
      wait_time    → time spent in READY state (used to compute avg. waiting time metric)
      arrival_time → process creation timestamp (fork() call time)
      start_time   → first CPU dispatch timestamp
      end_time     → process exit timestamp
      worker_id    → CPU/core ID this task is pinned to (analogous to `processor` in task_struct)
    """

    # --- Identity ---
    id:           str
    name:         str

    # --- CPU Burst Estimate ---
    # In a real OS, burst time is not known in advance; it's estimated using
    # exponential averaging: τ(n+1) = α·t(n) + (1-α)·τ(n) where t(n) is the
    # last actual burst and α is the smoothing factor (typically 0.5).
    burst_time:   float          # seconds

    # --- Scheduling Metadata ---
    priority:     Priority       = field(default=Priority.NORMAL)
    dependencies: List[str]      = field(default_factory=list)   # dependency PIDs

    # --- Resource Requirements ---
    cpu_cores:    float          = 1.0    # fractional cores supported (e.g. 0.5)
    memory_mb:    int            = 512    # megabytes of RAM requested

    # --- State ---
    status:       TaskStatus     = field(default=TaskStatus.PENDING)

    # --- MLFQ Queue Placement ---
    # Tracks which queue level (0/1/2) this task is currently in.
    # Starts at 0 (highest priority) and demotes down as it consumes full quanta.
    queue_level:  int            = 0

    # --- Accounting (OS scheduling metrics) ---
    # These mirror the fields in Linux's `sched_statistics` and /proc/<pid>/stat
    wait_time:    float          = 0.0   # cumulative time waiting in READY queue
    arrival_time: float          = field(default_factory=time.time)
    start_time:   Optional[float]= None  # set on first dispatch
    end_time:     Optional[float]= None  # set on COMPLETED/FAILED

    # --- Placement ---
    worker_id:    Optional[str]  = None  # which Worker this was scheduled onto

    # ------------------------------------------------------------------
    # Comparison support for heapq (SJF scheduler stores tasks in a min-heap)
    # ------------------------------------------------------------------
    def __lt__(self, other: "Task") -> bool:
        """
        heapq in Python is a min-heap ordered by the first element of the tuple.
        SJF needs to pop the task with the *smallest* burst_time first.
        Defining __lt__ lets us push Task objects directly into heapq without
        a wrapper tuple — Python will call this when comparing two Tasks.
        Ties are broken by arrival_time (FCFS among equal-burst tasks).
        """
        if self.burst_time == other.burst_time:
            return self.arrival_time < other.arrival_time
        return self.burst_time < other.burst_time

    # ------------------------------------------------------------------
    # Convenience
    # ------------------------------------------------------------------
    @property
    def turnaround_time(self) -> Optional[float]:
        """
        Turnaround time = end_time - arrival_time.
        Core OS scheduling metric: total wall-clock time from submission to completion.
        Little's Law: avg_turnaround = avg_waiting + avg_service_time.
        """
        if self.end_time is not None:
            return self.end_time - self.arrival_time
        return None

    def __repr__(self) -> str:
        return (
            f"Task(id={self.id!r}, name={self.name!r}, "
            f"burst={self.burst_time}s, priority={self.priority.name}, "
            f"status={self.status.name}, queue_level={self.queue_level})"
        )
