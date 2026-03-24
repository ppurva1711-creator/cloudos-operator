"""
resource_allocator.py — AntiGravity Scheduler: Resource Management
===================================================================

OS Analogy: Memory Allocator + CPU Affinity Manager + cgroups
--------------------------------------------------------------
This module mirrors three OS resource management subsystems:

  1. Memory Allocator (malloc / buddy system / slab allocator):
     Strategies like best-fit and worst-fit are classical memory placement
     policies studied since the 1960s. The same trade-offs apply here:
       - Best-fit:   minimise fragmentation (tight packing)
       - First-fit:  minimise search time (take first hole that fits)
       - Worst-fit:  maximise remaining free space per worker (spread load)

  2. CPU Affinity / Scheduler Domains (sched_setaffinity, taskset):
     We track which tasks run on which Worker (server/CPU), analogous to
     setting CPU affinity via taskset(1) or cgroup cpuset subsystem.

  3. Linux cgroups / ulimit — Resource Quota Enforcement:
     check_quota() mirrors Linux's cgroup resource limits (cpu.max, memory.limit_in_bytes)
     and the per-process ulimit settings (RLIMIT_CPU, RLIMIT_AS). Tasks exceeding
     the quota are rejected before execution — 'admission control'.

  4. Kubernetes Resource Requests & Limits:
     The Worker.can_fit() method mirrors Kubernetes' scheduler predicate
     'NodeResourcesFit' — a pod is only scheduled to a node if the node's
     allocatable CPU and memory can satisfy the pod's requests.

Worker Placement Strategies:
  - best_fit:   Minimise waste on each worker (pack tightly — reduces fragmentation)
  - first_fit:  Take the first worker that can fit the task (fast, O(n) scan)
  - worst_fit:  Pick the emptiest worker (spread load — better for burst absorption)
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Dict, List, Optional

from models import Task, TaskStatus


# ---------------------------------------------------------------------------
# Worker — represents a machine / CPU node in the cluster
# ---------------------------------------------------------------------------

@dataclass
class Worker:
    """
    Represents a physical or virtual machine in the worker pool.

    OS Analogy: A CPU node in a NUMA system, or a VM instance in a cloud region.
    Fields mirror the values visible in /proc/meminfo, /proc/cpuinfo,
    and Kubernetes node allocatable resources.

    Computed properties follow the Kubernetes resource model:
      allocatable = capacity - system_reserved - kube_reserved
    Here: free = total - used.
    """

    id:            str
    total_cpu:     float    # Total logical CPU cores (e.g., 4.0 = 4 vCPUs)
    total_mem:     int      # Total RAM in MB

    used_cpu:      float          = 0.0
    used_mem:      int            = 0
    running_tasks: List[str]      = field(default_factory=list)

    # ------------------------------------------------------------------
    # Computed Properties — mirrors /proc/meminfo 'MemAvailable'
    # ------------------------------------------------------------------

    @property
    def free_cpu(self) -> float:
        """
        Available CPU capacity.
        OS Analogy: 'idle' CPU time in /proc/stat, or Kubernetes node.allocatable.cpu.
        """
        return round(self.total_cpu - self.used_cpu, 4)

    @property
    def free_mem(self) -> int:
        """
        Available RAM in MB.
        OS Analogy: /proc/meminfo MemAvailable — not just MemFree but reclaimable.
        """
        return self.total_mem - self.used_mem

    @property
    def cpu_utilization(self) -> float:
        """
        CPU utilisation as a percentage.
        OS Analogy: top(1)'s %CPU column, averaged across cores.
        Formula: used / total * 100
        """
        if self.total_cpu == 0:
            return 0.0
        return round((self.used_cpu / self.total_cpu) * 100, 2)

    @property
    def mem_utilization(self) -> float:
        """
        Memory utilisation as a percentage.
        OS Analogy: free(1)'s 'used' column as percentage of total.
        """
        if self.total_mem == 0:
            return 0.0
        return round((self.used_mem / self.total_mem) * 100, 2)

    @property
    def is_idle(self) -> bool:
        """
        True if no tasks are running on this worker.
        OS Analogy: A CPU in the C0 idle state (halted, awaiting interrupt).
        """
        return len(self.running_tasks) == 0

    def can_fit(self, cpu: float, mem: int) -> bool:
        """
        Check if this worker has enough free resources for a task.

        OS Analogy: Kubernetes NodeResourcesFit predicate — 'fits' if:
          requested_cpu ≤ allocatable_cpu AND requested_mem ≤ allocatable_mem

        This is the admission control check run before ANY task is scheduled.
        """
        return self.free_cpu >= cpu and self.free_mem >= mem

    def __repr__(self) -> str:
        return (f"Worker({self.id}: CPU={self.used_cpu}/{self.total_cpu} "
                f"[{self.cpu_utilization}%] MEM={self.used_mem}/{self.total_mem}MB "
                f"[{self.mem_utilization}%] tasks={self.running_tasks})")


# ---------------------------------------------------------------------------
# ResourceAllocator
# ---------------------------------------------------------------------------

class ResourceAllocator:
    """
    Cluster-wide resource manager implementing three placement strategies.

    OS Analogy: Linux mm/mmap.c's mmap_region() (memory region placement) +
    Kubernetes scheduler's Filter→Score→Bind pipeline + Linux cgroups quota enforcement.

    The allocate() → release() lifecycle mirrors a kernel mutex:
      allocate() = mutex_lock()  → reserves resources
      release()  = mutex_unlock() → frees them for other tasks
    """

    def __init__(self, strategy: str = "best_fit") -> None:
        """
        strategy: one of "best_fit", "first_fit", "worst_fit".
        Mirrors choosing a memory allocator policy in glibc (MALLOC_TOP_PAD, MALLOC_MMAP_THRESHOLD)
        or Kubernetes scheduler profiles.
        """
        if strategy not in ("best_fit", "first_fit", "worst_fit"):
            raise ValueError(f"Unknown strategy '{strategy}'. "
                             f"Choose from: best_fit, first_fit, worst_fit")
        self.strategy: str = strategy
        self.workers:  Dict[str, Worker] = {}

    # ------------------------------------------------------------------
    def register_worker(self, worker: Worker) -> None:
        """
        Add a new worker to the pool.

        OS Analogy: Kubernetes node registration — a new kubelet contacts the API server
        and registers its capacity (cpu, memory, pods). The scheduler then considers it.
        In a NUMA system, this is analogous to topology learning during boot.
        """
        self.workers[worker.id] = worker
        print(f"[Alloc] Registered worker '{worker.id}' | "
              f"CPU={worker.total_cpu} cores | MEM={worker.total_mem}MB")

    # ------------------------------------------------------------------
    def remove_worker(self, worker_id: str) -> None:
        """
        Remove a worker from the pool (e.g., node failure, scale-in).

        OS Analogy: Kubernetes node eviction / drain (`kubectl drain`). Any tasks
        still on the removed worker would need to be rescheduled (we log a warning here).
        In a real system this triggers the controller's reconcile loop.
        """
        worker = self.workers.pop(worker_id, None)
        if worker:
            if worker.running_tasks:
                print(f"[Alloc] ⚠ Worker '{worker_id}' removed with running tasks: "
                      f"{worker.running_tasks} — these would need rescheduling!")
            else:
                print(f"[Alloc] Worker '{worker_id}' removed from pool")
        else:
            print(f"[Alloc] Worker '{worker_id}' not found in pool")

    # ------------------------------------------------------------------
    def check_quota(self, task: Task, max_cpu: float = 4.0, max_mem: int = 4096) -> bool:
        """
        Reject tasks that exceed resource limits before scheduling.

        OS Analogy: Linux ulimit / cgroup limits.
          ulimit -c (core file size)
          cgroup cpu.max (CPU bandwidth controller)
          cgroup memory.limit_in_bytes (hard memory limit)

        If a process exceeds its cgroup memory.limit_in_bytes, the OOM killer fires.
        Here we reject the task before it even runs — this is 'admission control',
        the scheduler-side equivalent of a firewall dropping oversized packets.

        Returns True if the task is WITHIN quota (acceptable), False if rejected.
        """
        cpu_ok = task.cpu_cores <= max_cpu
        mem_ok = task.memory_mb <= max_mem

        if not cpu_ok:
            print(f"[Alloc] ❌ QUOTA EXCEEDED for '{task.name}': "
                  f"requested {task.cpu_cores} cores > limit {max_cpu} cores")
            task.status = TaskStatus.REJECTED
        if not mem_ok:
            print(f"[Alloc] ❌ QUOTA EXCEEDED for '{task.name}': "
                  f"requested {task.memory_mb}MB > limit {max_mem}MB")
            task.status = TaskStatus.REJECTED

        if cpu_ok and mem_ok:
            print(f"[Alloc] ✓ Quota check passed for '{task.name}' "
                  f"(CPU={task.cpu_cores}/{max_cpu}, MEM={task.memory_mb}/{max_mem}MB)")
        return cpu_ok and mem_ok

    # ------------------------------------------------------------------
    def allocate(self, task: Task) -> Optional[Worker]:
        """
        Find a suitable worker and reserve resources for the task.

        OS Analogy: The kernel's physical page frame allocation (alloc_pages_node())
        combined with Kubernetes' scheduler Bind phase. Steps:
          1. Filter:  reject workers that can't fit the task (can_fit predicate)
          2. Score:   rank remaining workers using the chosen strategy
          3. Bind:    update accounting (used_cpu += task.cpu_cores, etc.)

        Returns the selected Worker, or None if no worker can accommodate the task.
        """
        if not self.workers:
            print(f"[Alloc] No workers registered — cannot allocate '{task.name}'")
            return None

        # --- Select worker based on strategy ---
        worker = None
        if self.strategy == "best_fit":
            worker = self._best_fit(task)
        elif self.strategy == "first_fit":
            worker = self._first_fit(task)
        elif self.strategy == "worst_fit":
            worker = self._worst_fit(task)

        if worker is None:
            print(f"[Alloc] ❌ No worker can fit '{task.name}' "
                  f"(needs CPU={task.cpu_cores}, MEM={task.memory_mb}MB) — INSUFFICIENT RESOURCES")
            return None

        # --- Commit the allocation ---
        # This is the 'bind' phase — resources are now reserved.
        worker.used_cpu += task.cpu_cores
        worker.used_mem += task.memory_mb
        worker.running_tasks.append(task.id)
        task.worker_id = worker.id
        task.status = TaskStatus.RUNNING

        print(f"[Alloc] ✅ Allocated '{task.name}' → worker '{worker.id}' "
              f"[strategy={self.strategy}] | "
              f"CPU: {worker.used_cpu}/{worker.total_cpu} ({worker.cpu_utilization}%) | "
              f"MEM: {worker.used_mem}/{worker.total_mem}MB ({worker.mem_utilization}%)")
        return worker

    # ------------------------------------------------------------------
    def release(self, task: Task) -> None:
        """
        Free resources when a task completes or fails.

        OS Analogy: free() / munmap() for memory, or cgroup accounting on process exit.
        In Kubernetes, when a Pod terminates, the node's allocatable resources are
        updated and the scheduler can place new Pods that were previously unschedulable.

        This is the critical 'resource reclaim' step. Without it, used_cpu and used_mem
        would grow monotonically → resource leak (analogous to a memory leak).
        """
        if task.worker_id is None:
            print(f"[Alloc] Cannot release '{task.name}' — no worker_id set")
            return

        worker = self.workers.get(task.worker_id)
        if worker is None:
            print(f"[Alloc] Worker '{task.worker_id}' not found — cannot release resources")
            return

        worker.used_cpu = max(0.0, worker.used_cpu - task.cpu_cores)
        worker.used_mem = max(0,   worker.used_mem - task.memory_mb)
        if task.id in worker.running_tasks:
            worker.running_tasks.remove(task.id)

        print(f"[Alloc] 🔓 Released resources for '{task.name}' from worker '{worker.id}' | "
              f"CPU now: {worker.used_cpu}/{worker.total_cpu} | "
              f"MEM now: {worker.used_mem}/{worker.total_mem}MB")

    # ------------------------------------------------------------------
    def _best_fit(self, task: Task) -> Optional[Worker]:
        """
        Best-Fit: choose the worker where the task fits with the LEAST waste.

        OS Analogy: Best-fit memory allocation — scans all free blocks and picks
        the tightest fit to minimise fragmentation. Introduced by Knuth in TAOCP Vol.1.

        Waste score = (free_cpu / total_cpu) + (free_mem / total_mem)
        Lower score = tighter fit = less wasted capacity.

        Trade-off: Reduces fragmentation but can create many small unusable 'holes'
        if tasks vary greatly in size. Also O(n) scan vs. O(1) for random placement.
        """
        best_worker: Optional[Worker] = None
        best_score: float = float('inf')

        for w in self.workers.values():
            if w.can_fit(task.cpu_cores, task.memory_mb):
                # Waste = leftover fraction after placing this task.
                after_cpu = (w.free_cpu - task.cpu_cores) / w.total_cpu
                after_mem = (w.free_mem - task.memory_mb) / w.total_mem
                score = after_cpu + after_mem  # lower = tighter fit
                if score < best_score:
                    best_score = score
                    best_worker = w

        if best_worker:
            print(f"[Alloc] Best-fit selected worker '{best_worker.id}' "
                  f"(waste score={best_score:.4f})")
        return best_worker

    # ------------------------------------------------------------------
    def _first_fit(self, task: Task) -> Optional[Worker]:
        """
        First-Fit: return the FIRST worker that can accommodate the task.

        OS Analogy: Early malloc implementations scanned the free list from the
        start and returned the first sufficiently large block. Fastest strategy
        (O(n) worst case but often terminates early) and simplest to implement.

        Trade-off: Can lead to front-loaded workers — workers registered first get
        disproportionately more tasks. Not ideal for load balancing.
        """
        for w in self.workers.values():
            if w.can_fit(task.cpu_cores, task.memory_mb):
                print(f"[Alloc] First-fit selected worker '{w.id}'")
                return w
        return None

    # ------------------------------------------------------------------
    def _worst_fit(self, task: Task) -> Optional[Worker]:
        """
        Worst-Fit: choose the worker with the MOST remaining capacity.

        OS Analogy: Worst-fit allocation — always picks the largest free block.
        The rationale is that after placing the task, the remaining hole is still
        large enough to serve future requests → avoids creating tiny unusable fragments.

        In distributed systems terms: this is the 'spread' scheduling strategy —
        prioritise empty nodes to maximise remaining capacity per node.
        Kubernetes uses this when the 'NodeResourcesLeastAllocated' scheduling plugin is active.

        Trade-off: Under-utilises workers (most capacity always held for future tasks),
        which can lead to poor resource efficiency if tasks often arrive in bursts.
        """
        best_worker: Optional[Worker] = None
        best_score: float = -1.0

        for w in self.workers.values():
            if w.can_fit(task.cpu_cores, task.memory_mb):
                # Score = total free fraction (higher = more empty = preferred)
                score = (w.free_cpu / w.total_cpu) + (w.free_mem / w.total_mem)
                if score > best_score:
                    best_score = score
                    best_worker = w

        if best_worker:
            print(f"[Alloc] Worst-fit selected worker '{best_worker.id}' "
                  f"(free score={best_score:.4f})")
        return best_worker

    # ------------------------------------------------------------------
    def total_cluster_capacity(self) -> dict:
        """
        Aggregate cluster-wide capacity and utilisation summary.

        OS Analogy: `kubectl top nodes` / `free -h` across all nodes.
        Used by cluster autoscalers to decide when to add or remove worker nodes.
        """
        total_cpu = sum(w.total_cpu for w in self.workers.values())
        used_cpu  = sum(w.used_cpu  for w in self.workers.values())
        total_mem = sum(w.total_mem for w in self.workers.values())
        used_mem  = sum(w.used_mem  for w in self.workers.values())

        return {
            "total_cpu":     total_cpu,
            "used_cpu":      used_cpu,
            "free_cpu":      round(total_cpu - used_cpu, 4),
            "cpu_util_pct":  round((used_cpu / total_cpu * 100) if total_cpu else 0, 2),
            "total_mem_mb":  total_mem,
            "used_mem_mb":   used_mem,
            "free_mem_mb":   total_mem - used_mem,
            "mem_util_pct":  round((used_mem / total_mem * 100) if total_mem else 0, 2),
            "worker_count":  len(self.workers),
        }

    # ------------------------------------------------------------------
    def print_cluster_state(self) -> None:
        """
        Pretty-print worker states with ASCII progress bars.

        OS Analogy: `htop` / `kubectl top nodes` with visual CPU and memory gauges.
        The ████░░░░ bars represent utilisation percentages like htop's bar graphs.

        Bar rendering: filled = int(utilisation% / 100 * BAR_WIDTH) chars
        """
        BAR_WIDTH = 10

        def make_bar(pct: float) -> str:
            """Build an ASCII progress bar like [████░░░░░░]."""
            filled = int(pct / 100 * BAR_WIDTH)
            empty  = BAR_WIDTH - filled
            return f"[{'█' * filled}{'░' * empty}]"

        capacity = self.total_cluster_capacity()

        print("\n" + "="*72)
        print("  🖥  AntiGravity Scheduler — Cluster State")
        print("="*72)
        print(f"  Workers: {capacity['worker_count']} | "
              f"Cluster CPU: {capacity['used_cpu']}/{capacity['total_cpu']} "
              f"({capacity['cpu_util_pct']}%) | "
              f"Cluster MEM: {capacity['used_mem_mb']}/{capacity['total_mem_mb']}MB "
              f"({capacity['mem_util_pct']}%)")
        print("-"*72)
        print(f"  {'Worker':<12} {'CPU Bar':<14} {'CPU%':>6} {'MEM Bar':<14} "
              f"{'MEM%':>6}  Running Tasks")
        print("-"*72)

        for w in self.workers.values():
            cpu_bar = make_bar(w.cpu_utilization)
            mem_bar = make_bar(w.mem_utilization)
            tasks   = ", ".join(w.running_tasks) if w.running_tasks else "(idle)"
            print(f"  {w.id:<12} {cpu_bar:<14} {w.cpu_utilization:>5.1f}% "
                  f"{mem_bar:<14} {w.mem_utilization:>5.1f}%  {tasks}")

        print("="*72 + "\n")
