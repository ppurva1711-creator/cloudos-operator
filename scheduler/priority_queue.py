<<<<<<< HEAD
"""
priority_queue.py — AntiGravity Scheduler: Scheduling Algorithm Implementations
================================================================================

OS Analogy: CPU Scheduling Algorithms
---------------------------------------
This module implements three classic OS scheduling algorithms plus a master
dispatcher class, directly mirroring the schedulers studied in OS theory:

  1. Round Robin (RR)  — Linux's CFS uses weighted round-robin with virtual runtime.
                         We use a fixed time_quantum to give each task a fair slice.

  2. Shortest Job First (SJF) — Provably optimal for minimising average waiting time
                                (by Smith's Rule). Non-preemptive version here; the
                                preemptive variant is called SRTF (Shortest Remaining Time First).

  3. Multi-Level Feedback Queue (MLFQ) — Used by Windows NT, macOS (XNU), and modern
                                          Linux via SCHED_OTHER. Tasks self-classify their
                                          burst behaviour through demotion, eliminating the
                                          need for advance burst-time knowledge.

  4. Scheduler (master) — Policy router analogous to Linux's pick_next_task() which
                          tries each sched_class in priority order.

Key OS concepts demonstrated:
  - Starvation & Aging      (SJF and MLFQ)
  - Priority Boost / Reset  (MLFQ _maybe_boost — Windows NT mechanism)
  - Time Quantum / Preemption (RR and MLFQ)
  - Turnaround & Waiting Time metrics (Scheduler.print_metrics)
"""

from __future__ import annotations

import heapq
import time
from collections import deque
from typing import Deque, List, Optional, Tuple

from models import Algorithm, Priority, Task, TaskStatus


# ---------------------------------------------------------------------------
# Constants (mirrors OS tunable parameters like /proc/sys/kernel/sched_*)
# ---------------------------------------------------------------------------

STARVATION_THRESHOLD: float = 10.0   # seconds before aging kicks in (SJF)
AGING_BOOST:          float = 0.5    # effective burst reduction per aging cycle
MLFQ_BOOST_INTERVAL:  float = 20.0  # seconds between full priority boosts (MLFQ)

MLFQ_QUANTUMS: Tuple[float, float, float] = (2.0, 4.0, 8.0)  # Q0, Q1, Q2


# =============================================================================
# 1. Round Robin Scheduler
# =============================================================================

class RoundRobinScheduler:
    """
    Round Robin (RR) — The fairness workhorse of time-sharing OSes.

    OS Background:
      - Invented for CTSS (Compatible Time-Sharing System) at MIT in the 1960s.
      - Linux CFS is a weighted round-robin over a red-black tree ordered by
        virtual runtime (vruntime); simpler systems use a plain FIFO circular queue.
      - Each process gets one time_quantum of CPU; if it doesn't finish, it is
        preempted and re-placed at the BACK of the ready queue.
      - Analogy: a fair token ring — every process gets a turn, no starvation possible.

    Trade-offs:
      - High context-switch overhead if quantum is too small (thrashing).
      - High avg. turnaround if quantum is too large (degenerates to FCFS).
      - Rule of thumb: quantum ≈ 80th percentile of typical CPU bursts.
    """

    def __init__(self, time_quantum: float = 2.0) -> None:
        # deque gives O(1) append and popleft — perfect circular buffer for RR.
        # In Linux this is analogous to the per-CPU run queue.
        self.queue:        Deque[Task] = deque()
        self.time_quantum: float       = time_quantum

    # ------------------------------------------------------------------
    def enqueue(self, task: Task) -> None:
        """
        Add a task to the back of the ready queue.

        OS Analogy: enqueue_task_fair() in Linux kernel — adds the process to the
        CFS run queue (rb_tree). Here we use deque.append() which is O(1).
        We print queue position to make the concurrency visible.
        """
        self.queue.append(task)
        position = len(self.queue)
        print(f"[RR] Enqueued '{task.name}' (id={task.id}) "
              f"→ position {position} in queue  [quantum={self.time_quantum}s]")

    # ------------------------------------------------------------------
    def next_task(self) -> Optional[Task]:
        """
        Dequeue the task at the front of the circular buffer.

        OS Analogy: pick_next_task_fair() — selects the process with the smallest
        vruntime (here: just the front of the deque). O(1) operation.
        Returns None when the queue is empty (CPU goes idle).
        """
        if not self.queue:
            # CPU idle — in a real OS the idle thread (swapper, PID 0) runs.
            print("[RR] Queue empty — CPU would go idle")
            return None
        task = self.queue.popleft()
        task.status = TaskStatus.RUNNING
        if task.start_time is None:
            # First dispatch — record response time (first_run - arrival_time).
            task.start_time = time.time()
        print(f"[RR] Dispatching '{task.name}' (id={task.id}) | burst={task.burst_time}s")
        return task

    # ------------------------------------------------------------------
    def requeue(self, task: Task) -> None:
        """
        Re-insert a task that consumed its full quantum without finishing.

        OS Analogy: This is the preemption path — the timer interrupt fires, the
        scheduler calls put_prev_task_fair(), which reinserts the process into the
        rb_tree with an updated vruntime. Here we simply append to the back of the
        deque to simulate FIFO round-robin ordering.

        The task's burst_time is decremented by the quantum consumed — this models
        'remaining burst' so we know when the task is truly finished.
        """
        task.burst_time = max(0.0, task.burst_time - self.time_quantum)
        task.status = TaskStatus.PENDING
        if task.burst_time > 0:
            self.queue.append(task)
            print(f"[RR] Requeued '{task.name}' | remaining burst={task.burst_time:.1f}s "
                  f"→ back of queue (position {len(self.queue)})")
        else:
            print(f"[RR] '{task.name}' finished within quantum — marking COMPLETED")
            task.status = TaskStatus.COMPLETED

    # ------------------------------------------------------------------
    @property
    def size(self) -> int:
        """Number of tasks currently waiting in the RR queue."""
        return len(self.queue)

    def stats(self) -> str:
        names = [t.name for t in self.queue]
        return f"[RR] Queue depth={self.size} | tasks={names}"


# =============================================================================
# 2. Shortest Job First (SJF) Scheduler
# =============================================================================

class SJFScheduler:
    """
    Shortest Job First (SJF) — Optimal non-preemptive algorithm for minimising
    average waiting time (proven by Smith's Rule, 1956).

    OS Background:
      - SJF is provably optimal for minimising average turnaround time among all
        non-preemptive algorithms — it is the greedy algorithm where 'shortest burst
        first' produces the globally optimal schedule.
      - Weakness: requires knowledge of burst time in advance. Real OSes approximate
        it via exponential moving average over past bursts.
      - Starvation risk: a continuous stream of short jobs can starve long jobs
        indefinitely. Aging is the classical fix.

    Aging:
      - If a task waits > STARVATION_THRESHOLD seconds, its effective burst_time is
        reduced by AGING_BOOST each time aging is applied, pushing it forward in the
        heap (smaller burst = higher priority).
      - This mirrors the aging technique in Multics and described in Tanenbaum's
        'Modern Operating Systems' §2.4.
    """

    def __init__(self) -> None:
        # heapq in Python is a min-heap. Task.__lt__ compares by burst_time,
        # so heappush / heappop give us SJF ordering.
        self._heap: List[Task] = []
        self._last_aging_check: float = time.time()

    # ------------------------------------------------------------------
    def enqueue(self, task: Task) -> None:
        """
        Push task onto the min-heap ordered by burst_time.

        heapq.heappush is O(log n) — same asymptotic cost as inserting into any
        priority queue. Task.__lt__ drives the heap ordering by burst_time.
        """
        heapq.heappush(self._heap, task)
        print(f"[SJF] Enqueued '{task.name}' (id={task.id}) | burst={task.burst_time}s "
              f"| heap size={len(self._heap)}")

    # ------------------------------------------------------------------
    def next_task(self) -> Optional[Task]:
        """
        Pop the task with the minimum burst_time — O(log n) heap operation.

        Before popping, apply aging to prevent starvation:
          - Walk all waiting tasks.
          - Any task whose wait_time exceeds STARVATION_THRESHOLD gets its
            burst_time reduced, then the heap is re-sifted (heapify).
        This simulates what a real OS does when boosting a starving process's
        priority — here we reduce burst_time to achieve the same 'move forward' effect.
        """
        if not self._heap:
            print("[SJF] Heap empty — CPU would go idle")
            return None

        # --- Aging pass ---
        # OS kernels run the aging calculation periodically (e.g., in the timer
        # interrupt handler in BSD, or the CFS load-balancer tick in Linux).
        now = time.time()
        aged = False
        for task in self._heap:
            task.wait_time += (now - self._last_aging_check)
            if task.wait_time > STARVATION_THRESHOLD:
                old_burst = task.burst_time
                task.burst_time = max(0.1, task.burst_time - AGING_BOOST)
                print(f"[SJF] ⏳ AGING applied to '{task.name}': "
                      f"burst {old_burst:.2f}s → {task.burst_time:.2f}s "
                      f"(waited {task.wait_time:.1f}s > threshold {STARVATION_THRESHOLD}s)")
                aged = True
        if aged:
            # Re-establish heap invariant after modifying keys in place.
            heapq.heapify(self._heap)
        self._last_aging_check = now

        task = heapq.heappop(self._heap)
        task.status = TaskStatus.RUNNING
        if task.start_time is None:
            task.start_time = time.time()
        print(f"[SJF] Dispatching '{task.name}' (id={task.id}) | burst={task.burst_time}s "
              f"(shortest in heap)")
        return task

    # ------------------------------------------------------------------
    @property
    def size(self) -> int:
        return len(self._heap)

    def stats(self) -> str:
        sorted_tasks = sorted(self._heap)  # ascending burst_time
        names_bursts = [(t.name, t.burst_time) for t in sorted_tasks]
        return f"[SJF] Heap size={self.size} | order (shortest first)={names_bursts}"


# =============================================================================
# 3. Multi-Level Feedback Queue (MLFQ) Scheduler
# =============================================================================

class MLFQScheduler:
    """
    Multi-Level Feedback Queue (MLFQ) — the most sophisticated general-purpose
    scheduler, used by Windows NT, macOS (XNU), and modern Linux (SCHED_OTHER class).

    OS Background:
      Proposed by Corbató (1962) for CTSS. MLFQ solves the core scheduling dilemma:
        - We want SJF's efficiency but DON'T know burst times in advance.
        - We want RR's fairness but don't want interactive tasks to wait behind long jobs.

      MLFQ's insight: let tasks REVEAL their burst behaviour through observed CPU usage:
        - Tasks that use their full quantum → IO-bound or interactive → stay/promote to Q0.
        - Tasks that burn through Q0 → Q1 → Q2 are CPU-bound → run less frequently.
        - This achieves adaptive priority without prior knowledge.

    Three Queues:
      Q0 (quantum=2s)  — Short bursts / interactive — highest priority
      Q1 (quantum=4s)  — Medium bursts
      Q2 (quantum=8s)  — Long CPU-bound jobs — lowest priority (like BATCH class)

    Priority Boost (Windows NT mechanism):
      Every MLFQ_BOOST_INTERVAL seconds, ALL tasks are reset to Q0. This prevents
      starvation of long-running tasks that have sunk to Q2. Windows NT originally
      had a similar mechanism called 'priority boost' for threads that had been
      waiting too long.

    Rules (from Arpaci-Dusseau 'OSTEP' textbook, Chapter 8):
      Rule 1: If Priority(A) > Priority(B) → A runs.
      Rule 2: If Priority(A) == Priority(B) → A & B run in RR.
      Rule 3: New jobs enter at the highest priority (Q0).
      Rule 4: Once a job uses up its allotment at a given level, its priority is reduced.
      Rule 5 (Boost): After period S, move all jobs to Q0.
    """

    def __init__(self) -> None:
        # Three queues — each a deque for O(1) append/popleft.
        self.queues: List[Deque[Task]] = [deque(), deque(), deque()]
        self._last_boost: float = time.time()

    # ------------------------------------------------------------------
    def enqueue(self, task: Task, level: int = 0) -> None:
        """
        Place a new task at the specified queue level.

        MLFQ Rule 3: new jobs always enter at Q0 (highest priority).
        This gives them a chance to prove they are short/interactive.
        If they consume the full Q0 quantum, demote() will push them to Q1.
        """
        task.queue_level = level
        self.queues[level].append(task)
        q_size = len(self.queues[level])
        print(f"[MLFQ] Enqueued '{task.name}' (id={task.id}) → Q{level} "
              f"(quantum={MLFQ_QUANTUMS[level]}s) | Q{level} depth={q_size}")

    # ------------------------------------------------------------------
    def demote(self, task: Task) -> None:
        """
        Move a task DOWN one queue level after it consumed its full quantum.

        MLFQ Rule 4: using the full allotment is evidence of CPU-intensive
        behaviour → reduce priority. The task's burst_time is decremented by the
        quantum consumed at its current level.

        OS Analogy: Linux's SCHED_OTHER priority decay — processes that consistently
        burn CPU get lower dynamic priority in the epoch scheduler.
        """
        consumed_quantum = MLFQ_QUANTUMS[task.queue_level]
        task.burst_time = max(0.0, task.burst_time - consumed_quantum)

        if task.burst_time <= 0:
            print(f"[MLFQ] '{task.name}' completed at Q{task.queue_level} — no demotion needed")
            task.status = TaskStatus.COMPLETED
            return

        new_level = min(task.queue_level + 1, 2)  # clamp at Q2 (bottom)
        old_level = task.queue_level
        print(f"[MLFQ] DEMOTE '{task.name}': Q{old_level} → Q{new_level} "
              f"(used full quantum={consumed_quantum}s, remaining={task.burst_time:.1f}s)")
        self.enqueue(task, new_level)

    # ------------------------------------------------------------------
    def requeue_same_level(self, task: Task) -> None:
        """
        Re-insert a task at its CURRENT queue level when it yields voluntarily
        (e.g., waiting for I/O, blocking on a lock).

        MLFQ insight: a task that gives up the CPU before its quantum expires is
        likely I/O-bound or interactive. It should keep its current priority —
        penalising voluntary yields would incentivise processes to never block,
        which would starve truly interactive work.

        OS Analogy: Linux's voluntary preemption path — cond_resched() / schedule()
        called from syscall context. The task stays at the same nice level.
        """
        print(f"[MLFQ] '{task.name}' yielded voluntarily → requeued at same Q{task.queue_level}")
        self.queues[task.queue_level].append(task)

    # ------------------------------------------------------------------
    def _maybe_boost(self) -> None:
        """
        Periodic Priority Boost — move ALL tasks back to Q0.

        This is the 'Rule 5' boost from OSTEP (Arpaci-Dusseau) and directly mirrors
        the Windows NT priority boost mechanism for starved threads.

        Why is this necessary?
          Without boosting, a CPU-bound task that settles into Q2 will only run when
          Q0 and Q1 are empty — which may be never in a busy system. This is classic
          starvation. Resetting everyone to Q0 every S seconds gives every task a
          fresh chance and prevents indefinite starvation (for S large enough not to
          thrash, small enough to prevent starvation).

        OS Analogy: Windows NT sets the 'priority ceiling' of threads boosted via
        PriorityBoost. Linux CFS handles this differently with load balancing and
        the sched_latency tunable.
        """
        now = time.time()
        if now - self._last_boost < MLFQ_BOOST_INTERVAL:
            return  # Not time yet

        boosted_count = 0
        # Drain Q1 and Q2, re-enqueue everything at Q0.
        for level in [1, 2]:
            while self.queues[level]:
                task = self.queues[level].popleft()
                task.queue_level = 0
                self.queues[0].append(task)
                boosted_count += 1

        if boosted_count > 0:
            print(f"[MLFQ] 🚀 PRIORITY BOOST — moved {boosted_count} tasks back to Q0 "
                  f"(starvation prevention, interval={MLFQ_BOOST_INTERVAL}s)")
        self._last_boost = now

    # ------------------------------------------------------------------
    def next_task(self) -> Optional[Task]:
        """
        Dispatch the highest-priority ready task.

        MLFQ Rule 1: service Q0 before Q1 before Q2.
        Before picking, check if a priority boost is due (_maybe_boost).

        OS Analogy: Linux's pick_next_task() iterates sched_class list in priority
        order: stop → dl → rt → fair → idle. We do the same across our three queues.
        """
        self._maybe_boost()

        for level, queue in enumerate(self.queues):
            if queue:
                task = queue.popleft()
                task.status = TaskStatus.RUNNING
                if task.start_time is None:
                    task.start_time = time.time()
                print(f"[MLFQ] Dispatching '{task.name}' from Q{level} "
                      f"(quantum={MLFQ_QUANTUMS[level]}s, burst={task.burst_time}s)")
                return task

        print("[MLFQ] All queues empty — CPU would go idle")
        return None

    # ------------------------------------------------------------------
    def stats(self) -> str:
        details = {f"Q{i}": [t.name for t in q] for i, q in enumerate(self.queues)}
        total = sum(len(q) for q in self.queues)
        return f"[MLFQ] Total={total} tasks | {details}"

    @property
    def size(self) -> int:
        return sum(len(q) for q in self.queues)

    @property
    def q0_size(self) -> int:
        return len(self.queues[0])


# =============================================================================
# 4. Master Scheduler (Policy Router)
# =============================================================================

class Scheduler:
    """
    Master Scheduler — auto-routes tasks to the appropriate sub-scheduler.

    OS Analogy: Linux's __schedule() / pick_next_task() which polls each sched_class
    (stop → deadline → rt → fair → idle) in priority order. Our Scheduler similarly
    checks MLFQ Q0, then SJF, then RR, then lower MLFQ queues.

    Routing Policy (at submit time):
      CRITICAL priority    → MLFQ  (real-time-like, needs adaptive scheduling)
      burst_time ≤ 3s      → SJF   (short jobs benefit most from SJF's optimality)
      burst_time ≥ 10s     → RR    (long jobs: RR prevents a single job hogging CPU)
      everything else      → MLFQ  (adaptive for unknown behaviour)

    Dispatch Order (next_task priority):
      1. MLFQ Q0  — highest priority / interactive / new tasks
      2. SJF      — short jobs (once we know they're short, run them promptly)
      3. RR       — long-running fair-share jobs
      4. MLFQ Q1  — medium-priority demoted tasks
      5. MLFQ Q2  — background/batch tasks (lowest)

    Metrics:
      avg_wait_time      = Σ(wait_time) / n_completed_tasks
      avg_turnaround_time = Σ(turnaround_time) / n_completed_tasks
      (Little's Law: E[T] = E[W] + E[S], where S is service time / burst)
    """

    def __init__(self) -> None:
        self.rr:   RoundRobinScheduler = RoundRobinScheduler()
        self.sjf:  SJFScheduler        = SJFScheduler()
        self.mlfq: MLFQScheduler       = MLFQScheduler()

        self._completed_tasks: List[Task] = []

    # ------------------------------------------------------------------
    def submit(self, task: Task) -> Algorithm:
        """
        Route a newly arrived task to the correct sub-scheduler.

        OS Analogy: sched_setscheduler() — assigning a scheduling policy to a process.
        In Linux you can call `chrt` or `sched_setattr()` to set SCHED_FIFO / SCHED_RR
        / SCHED_OTHER. Here the master Scheduler makes the decision automatically based
        on observable task characteristics (priority and estimated burst time).
        """
        task.arrival_time = time.time()
        task.status = TaskStatus.PENDING

        # --- Routing logic ---
        # CRITICAL tasks get MLFQ for adaptive, low-latency scheduling.
        if task.priority == Priority.CRITICAL:
            algo = Algorithm.MLFQ
            self.mlfq.enqueue(task, level=0)

        # Short tasks (≤3s) benefit most from SJF — minimises their wait.
        elif task.burst_time <= 3.0:
            algo = Algorithm.SJF
            self.sjf.enqueue(task)

        # Long tasks (≥10s) go to RR for fair time-slicing.
        elif task.burst_time >= 10.0:
            algo = Algorithm.ROUND_ROBIN
            self.rr.enqueue(task)

        # All others → MLFQ for adaptive classification.
        else:
            algo = Algorithm.MLFQ
            self.mlfq.enqueue(task, level=0)

        print(f"[Scheduler] Submitted '{task.name}' → {algo.name} "
              f"(priority={task.priority.name}, burst={task.burst_time}s)")
        return algo

    # ------------------------------------------------------------------
    def requeue(self, task: Task, algo: Algorithm) -> None:
        """
        Re-insert a task into its original sub-scheduler.

        OS Analogy: This is the task re-queuing path after a failed allocation
        or a preemption event. In Linux, if a process cannot be scheduled on a
        particular processor, it is kept in the run queue for the next scheduler tick.
        """
        task.status = TaskStatus.PENDING
        
        if algo == Algorithm.MLFQ:
            # For MLFQ we MUST preserve the current queue level
            self.mlfq.enqueue(task, level=task.queue_level)
        elif algo == Algorithm.SJF:
            self.sjf.enqueue(task)
        elif algo == Algorithm.ROUND_ROBIN:
            self.rr.enqueue(task)
        
        print(f"[Scheduler] Requeued '{task.name}' back to {algo.name} (level {task.queue_level})")

    # ------------------------------------------------------------------
    def next_task(self) -> Optional[Task]:
        """
        Select the next task to run, honouring the global priority order:
          MLFQ Q0 > SJF > RR > MLFQ Q1 > MLFQ Q2

        OS Analogy: pick_next_task() in Linux iterates sched_class objects in
        decreasing priority. Our hierarchy puts MLFQ-Q0 (interactive) first,
        SJF (short jobs) second, RR (fair long jobs) third, lower MLFQ levels last.

        Returns None if all queues are empty (CPU idle).
        """

        # 1. MLFQ Q0 — highest priority (interactive / CRITICAL)
        if self.mlfq.queues[0]:
            return self.mlfq.next_task()

        # 2. SJF — short jobs
        if self.sjf.size > 0:
            return self.sjf.next_task()

        # 3. Round Robin — long fair-share jobs
        if self.rr.size > 0:
            return self.rr.next_task()

        # 4. MLFQ Q1 / Q2 — demoted background tasks
        if self.mlfq.size > 0:
            return self.mlfq.next_task()

        print("[Scheduler] All queues empty — system idle")
        return None

    # ------------------------------------------------------------------
    def complete_task(self, task: Task) -> None:
        """
        Mark a task as completed and record its accounting data.

        OS Analogy: do_exit() in Linux — frees task_struct, updates wait4() info,
        records task accounting in acct(2) format (if process accounting is enabled),
        sends SIGCHLD to parent.

        Here we record end_time and compute wait_time before archiving the task
        in completed_tasks for metrics reporting.
        """
        task.end_time = time.time()
        task.status = TaskStatus.COMPLETED
        # wait_time = time between arrival and first dispatch (response time)
        if task.start_time is not None:
            task.wait_time = task.start_time - task.arrival_time
        self._completed_tasks.append(task)
        print(f"[Scheduler] ✅ '{task.name}' COMPLETED | "
              f"wait={task.wait_time:.2f}s | "
              f"turnaround={task.turnaround_time:.2f}s")

    # ------------------------------------------------------------------
    def print_metrics(self) -> None:
        """
        Print average scheduling metrics across all completed tasks.

        OS Analogy: /proc/schedstat, getrusage(2), and Linux perf sched commands
        expose per-CPU and per-process scheduling statistics. We compute:

          Average Waiting Time    = Σ(start_time - arrival_time) / n
          Average Turnaround Time = Σ(end_time   - arrival_time) / n

        These are the two primary metrics used to compare scheduling algorithms
        in OS textbooks (Silberschatz §5, Tanenbaum §2, OSTEP §7).
        """
        n = len(self._completed_tasks)
        if n == 0:
            print("[Scheduler] No completed tasks yet — no metrics available")
            return

        avg_wait = sum(t.wait_time for t in self._completed_tasks) / n
        avg_ta   = sum(t.turnaround_time for t in self._completed_tasks
                       if t.turnaround_time is not None) / n

        print("\n" + "="*60)
        print("  📊 AntiGravity Scheduler — Performance Metrics")
        print("="*60)
        print(f"  Completed tasks      : {n}")
        print(f"  Avg Waiting Time     : {avg_wait:.4f}s  (time in READY queue)")
        print(f"  Avg Turnaround Time  : {avg_ta:.4f}s  (arrival → completion)")
        print(f"  Avg Service Time (est): {avg_ta - avg_wait:.4f}s  (turnaround - wait)")
        print("="*60)
        print("  Per-task breakdown:")
        header = f"  {'Task':<25} {'Wait':>8} {'Turnaround':>12} {'Status'}"
        print(header)
        print("  " + "-"*60)
        for t in self._completed_tasks:
            ta = f"{t.turnaround_time:.4f}s" if t.turnaround_time else "N/A"
            print(f"  {t.name:<25} {t.wait_time:>8.4f}s {ta:>12}   {t.status.name}")
        print("="*60 + "\n")
=======
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
>>>>>>> caca5ef8523ac798db74420ad576e21faf9435d1
