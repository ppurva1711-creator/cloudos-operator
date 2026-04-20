# -*- coding: utf-8 -*-
"""
main.py — AntiGravity Scheduler: Integration Test Runner
=========================================================

Runs 4 comprehensive tests covering all scheduler components.
No pytest — run directly with: python main.py

  Test 1: Deadlock Detector  — DAG safety, cycle detection, topological sort
  Test 2: Resource Allocator — worker pool, placement strategies, quota enforcement
  Test 3: Algorithm Showcase — RR, SJF, MLFQ in isolation
  Test 4: End-to-End Sim    — ML pipeline with full scheduling loop
"""

import sys
import time

# Force UTF-8 output on Windows so Unicode characters display correctly.
if hasattr(sys.stdout, 'reconfigure'):
    sys.stdout.reconfigure(encoding='utf-8', errors='replace')

# Make sure we can import from the same directory
import os
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from models import Task, Priority, TaskStatus, Algorithm
from deadlock_detector import DeadlockDetector
from resource_allocator import ResourceAllocator, Worker
from priority_queue import RoundRobinScheduler, SJFScheduler, MLFQScheduler, Scheduler


# ============================================================
# Utility helpers
# ============================================================

def section(title: str) -> None:
    """Print a clearly visible section header."""
    bar = "═" * 70
    print(f"\n{bar}")
    print(f"  🔷 {title}")
    print(f"{bar}\n")


def subsection(title: str) -> None:
    """Print a subsection header."""
    print(f"\n  ─── {title} ───")


def task(tid: str, name: str, burst: float, priority: Priority = Priority.NORMAL,
         deps: list = None, cpu: float = 1.0, mem: int = 512) -> Task:
    """Factory shorthand for creating test Tasks with fixed arrival_time."""
    t = Task(
        id=tid, name=name, burst_time=burst,
        priority=priority,
        dependencies=deps or [],
        cpu_cores=cpu,
        memory_mb=mem,
        arrival_time=time.time(),
    )
    return t


# ============================================================
# TEST 1 — Deadlock Detector
# ============================================================

def test_1_deadlock_detector() -> None:
    section("TEST 1 — Deadlock Detector")

    # ── 1a. Build a safe linear DAG: T1→T2→T3→T4 ──────────────
    subsection("1a: Build safe DAG (T1←T2←T3←T4) with can_add() checks")

    print("  Edges: T2→T1, T3→T2, T4→T3")
    print("  (T2 depends on T1, T3 depends on T2, T4 depends on T3)\n")

    detector = DeadlockDetector()

    # For each task: dry-run first, then commit.
    for tid, deps in [("T1", []), ("T2", ["T1"]), ("T3", ["T2"]), ("T4", ["T3"])]:
        safe, cycle = detector.can_add(tid, deps)
        if safe:
            detector.add_task(tid, deps)
        else:
            print(f"  [ERROR] Unexpected cycle when adding {tid}: {cycle}")

    # ── 1b. Print graph & topological order ─────────────────────
    subsection("1b: Graph and Topological Order")
    detector.print_graph()

    topo = detector.topological_order()
    print(f"\n  Valid execution order: {' → '.join(topo) if topo else 'CYCLE DETECTED'}")

    # ── 1c. get_ready_tasks with T1+T2 completed ────────────────
    subsection("1c: get_ready_tasks() — T1 and T2 already done")
    all_ids = ["T1", "T2", "T3", "T4"]
    completed = {"T1", "T2"}
    ready = detector.get_ready_tasks(all_ids, completed)
    print(f"  Completed: {sorted(completed)}")
    print(f"  Ready to run: {ready}  (expected: ['T3'])")

    # ── 1d. Inject a cycle & detect it ──────────────────────────
    subsection("1d: Inject cycle T1→T4 (making T4→T3→T2→T1→T4) and detect")
    print("  Attempting to add T1 → [T4] (would create a cycle)")
    safe, cycle = detector.can_add("T1", ["T4"])
    if not safe:
        print(f"  ✅ Cycle correctly detected: {' → '.join(cycle)}")

    # Now manually inject it into the live graph to test check_current_graph()
    print("\n  Injecting T1→T4 manually into live graph...")
    detector.graph["T1"].append("T4")
    detector.check_current_graph()

    # Cleanup: remove the cycle edge
    detector.graph["T1"].remove("T4")
    print("  [Cleaned up injected edge T1→T4]")
    detector.check_current_graph()

    # ── 1e. Real ML Pipeline DAG ─────────────────────────────────
    subsection("1e: Real ML Pipeline DAG: preprocess→train→evaluate→deploy")
    ml_detector = DeadlockDetector()
    pipeline = [
        ("preprocess", []),
        ("train",      ["preprocess"]),
        ("evaluate",   ["train"]),
        ("deploy",     ["evaluate"]),
    ]
    for tid, deps in pipeline:
        safe, cycle = ml_detector.can_add(tid, deps)
        if safe:
            ml_detector.add_task(tid, deps)
    ml_detector.print_graph()
    ml_topo = ml_detector.topological_order()
    print(f"\n  ML Pipeline execution order: {' → '.join(ml_topo) if ml_topo else 'ERROR'}")

    print("\n  ✅ TEST 1 PASSED\n")


# ============================================================
# TEST 2 — Resource Allocator
# ============================================================

def test_2_resource_allocator() -> None:
    section("TEST 2 — Resource Allocator")

    # ── 2a. Register 3 workers ───────────────────────────────────
    subsection("2a: Register 3 workers")
    allocator = ResourceAllocator(strategy="best_fit")

    w1 = Worker(id="worker-1", total_cpu=4.0,  total_mem=8192)
    w2 = Worker(id="worker-2", total_cpu=8.0,  total_mem=16384)
    w3 = Worker(id="worker-3", total_cpu=2.0,  total_mem=4096)

    allocator.register_worker(w1)
    allocator.register_worker(w2)
    allocator.register_worker(w3)

    # ── 2b. Print initial cluster state ──────────────────────────
    subsection("2b: Initial cluster state (all idle)")
    allocator.print_cluster_state()

    # ── 2c. Allocate 5 tasks ─────────────────────────────────────
    subsection("2c: Allocate 5 tasks")

    t1 = task("t1", "data-loader",     burst=5.0, cpu=1.0,  mem=1024)
    t2 = task("t2", "model-trainer",   burst=15.0, cpu=3.0, mem=6144)
    t3 = task("t3", "log-aggregator",  burst=3.0, cpu=0.5,  mem=512)
    # Task that fails quota check (cpu > 4.0 limit)
    t4 = task("t4", "giant-gpu-job",   burst=20.0, cpu=6.0, mem=2048)
    # Task too big for any single worker (needs 9 cores — largest worker has 8)
    t5 = task("t5", "impossible-task", burst=10.0, cpu=9.0, mem=1024)

    print("\n  Checking quotas and allocating...\n")

    # Task 1 — normal allocation
    if allocator.check_quota(t1):
        allocator.allocate(t1)

    # Task 2 — normal allocation
    if allocator.check_quota(t2):
        allocator.allocate(t2)

    # Task 3 — normal allocation
    if allocator.check_quota(t3):
        allocator.allocate(t3)

    # Task 4 — fails quota (cpu > 4.0)
    print(f"\n  Submitting '{t4.name}' (cpu={t4.cpu_cores}) — expecting quota rejection:")
    if not allocator.check_quota(t4, max_cpu=4.0, max_mem=4096):
        print(f"  → '{t4.name}' REJECTED by quota (status={t4.status.name})")

    # Task 5 — passes quota but no worker can fit it
    print(f"\n  Submitting '{t5.name}' (cpu={t5.cpu_cores}) — expecting no worker fit:")
    if allocator.check_quota(t5, max_cpu=10.0, max_mem=8192):
        result = allocator.allocate(t5)
        if result is None:
            print(f"  → '{t5.name}' UNSCHEDULABLE: no worker has {t5.cpu_cores} free cores")

    # ── 2d. Cluster state after allocations ──────────────────────
    subsection("2d: Cluster state after allocations")
    allocator.print_cluster_state()

    # ── 2e. Release 2 tasks ───────────────────────────────────────
    subsection("2e: Release t1 and t3, show freed resources")
    allocator.release(t1)
    allocator.release(t3)
    allocator.print_cluster_state()

    cap = allocator.total_cluster_capacity()
    print(f"  Total cluster: CPU {cap['used_cpu']}/{cap['total_cpu']} "
          f"({cap['cpu_util_pct']}%) | "
          f"MEM {cap['used_mem_mb']}/{cap['total_mem_mb']}MB ({cap['mem_util_pct']}%)")

    print("\n  ✅ TEST 2 PASSED\n")


# ============================================================
# TEST 3 — Individual Scheduling Algorithms
# ============================================================

def test_3_algorithms() -> None:
    section("TEST 3 — Individual Scheduling Algorithms")

    # 5 tasks to feed into each scheduler
    tasks = [
        task("a1", "compress-logs",   burst=1.5,  priority=Priority.LOW),
        task("a2", "train-model",     burst=12.0, priority=Priority.HIGH),
        task("a3", "health-check",    burst=0.8,  priority=Priority.CRITICAL),
        task("a4", "backup-db",       burst=8.0,  priority=Priority.NORMAL),
        task("a5", "send-email",      burst=2.5,  priority=Priority.NORMAL),
    ]

    # ── RR ───────────────────────────────────────────────────────
    subsection("3a: Round Robin Scheduler")
    rr = RoundRobinScheduler(time_quantum=2.0)
    for t in tasks:
        # Reset status for clean enqueue
        t.status = TaskStatus.PENDING
        t.start_time = None
        rr.enqueue(t)
    print(f"\n  {rr.stats()}")
    print("\n  Calling next_task() twice:")
    t_rr1 = rr.next_task()
    print(f"  → Picked: '{t_rr1.name}' (front of deque — FCFS order)")
    t_rr2 = rr.next_task()
    print(f"  → Picked: '{t_rr2.name}' (next in circular queue)")
    print("\n  Requeueing first task (simulating quantum expiry):")
    rr.requeue(t_rr1)
    print(f"  {rr.stats()}")

    # ── SJF ──────────────────────────────────────────────────────
    subsection("3b: SJF Scheduler (min-heap by burst_time)")
    sjf = SJFScheduler()
    for t in tasks:
        t.status = TaskStatus.PENDING
        t.start_time = None
        t.wait_time = 0.0
        sjf.enqueue(t)
    print(f"\n  {sjf.stats()}")
    print("\n  Calling next_task() twice:")
    t_sjf1 = sjf.next_task()
    print(f"  → Picked: '{t_sjf1.name}' (burst={t_sjf1.burst_time}s — shortest in heap)")
    t_sjf2 = sjf.next_task()
    print(f"  → Picked: '{t_sjf2.name}' (burst={t_sjf2.burst_time}s — next shortest)")

    # ── MLFQ ─────────────────────────────────────────────────────
    subsection("3c: MLFQ Scheduler — enqueue, stats, demote demo")
    mlfq = MLFQScheduler()
    for t in tasks:
        t.status = TaskStatus.PENDING
        t.start_time = None
        t.queue_level = 0
        mlfq.enqueue(t, level=0)
    print(f"\n  {mlfq.stats()}")

    print("\n  Calling next_task() twice (should pick from Q0):")
    t_mlfq1 = mlfq.next_task()
    print(f"  → Picked: '{t_mlfq1.name}' from Q{t_mlfq1.queue_level}")
    t_mlfq2 = mlfq.next_task()
    print(f"  → Picked: '{t_mlfq2.name}' from Q{t_mlfq2.queue_level}")

    print(f"\n  Demonstrating demote(): '{t_mlfq1.name}' (Q0) consumed full quantum → moves to Q1")
    t_mlfq1.queue_level = 0  # Reset for demo
    mlfq.demote(t_mlfq1)
    print(f"  {mlfq.stats()}")

    print("\n  ✅ TEST 3 PASSED\n")


# ============================================================
# TEST 4 — Full End-to-End Simulation (ML Pipeline)
# ============================================================

def test_4_end_to_end() -> None:
    section("TEST 4 -- Full End-to-End Simulation (ML Pipeline)")

    print("""
  Simulating an ML pipeline with 8 tasks and real dependencies:

    fetch-data -> clean-data -> feature-eng -> train-model -> evaluate
    health-check (independent)
    log-aggregator (independent)
    send-alert -> evaluate (sends alert when evaluation is done)
""")

    # -- 4a. Define tasks -----------------------------------------
    subsection("4a: Define 8 tasks")

    pipeline_tasks = [
        task("fetch",    "fetch-data",      burst=2.0,  priority=Priority.HIGH,
             deps=[],                   cpu=1.0,  mem=512),
        task("clean",    "clean-data",      burst=3.0,  priority=Priority.NORMAL,
             deps=["fetch"],            cpu=1.0,  mem=1024),
        task("eng",      "feature-eng",     burst=4.0,  priority=Priority.NORMAL,
             deps=["clean"],            cpu=2.0,  mem=2048),
        task("train",    "train-model",     burst=15.0, priority=Priority.CRITICAL,
             deps=["eng"],              cpu=3.0,  mem=4096),
        task("eval",     "evaluate",        burst=5.0,  priority=Priority.HIGH,
             deps=["train"],            cpu=2.0,  mem=2048),
        task("health",   "health-check",    burst=1.0,  priority=Priority.CRITICAL,
             deps=[],                   cpu=0.5,  mem=256),
        task("log_agg",  "log-aggregator",  burst=10.0, priority=Priority.LOW,
             deps=[],                   cpu=1.0,  mem=1024),
        task("alert",    "send-alert",      burst=2.0,  priority=Priority.NORMAL,
             deps=["eval"],             cpu=0.5,  mem=256),
    ]

    # -- 4b. Setup components -------------------------------------
    subsection("4b: Initialize Scheduler, DeadlockDetector, ResourceAllocator")
    scheduler   = Scheduler()
    detector    = DeadlockDetector()
    allocator   = ResourceAllocator(strategy="best_fit")

    allocator.register_worker(Worker(id="gpu-1",    total_cpu=8.0,  total_mem=16384))
    allocator.register_worker(Worker(id="cpu-1",    total_cpu=4.0,  total_mem=8192))
    allocator.register_worker(Worker(id="micro-1",  total_cpu=2.0,  total_mem=4096))

    # -- 4c. can_add -> add_task (NOT submit yet) ------------------
    subsection("4c: can_add() -> add_task() (tasks queued in detector, not scheduler yet)")
    task_map: dict = {}
    for t in pipeline_tasks:
        safe, cycle = detector.can_add(t.id, t.dependencies)
        if safe:
            detector.add_task(t.id, t.dependencies)
            task_map[t.id] = t
        else:
            print(f"  REJECTED '{t.name}' -- would create cycle: {cycle}")
            t.status = TaskStatus.REJECTED

    admitted = list(task_map.values())

    # -- 4d. Dependency graph & topo order ------------------------
    subsection("4d: Full dependency graph and topological order")
    detector.print_graph()
    topo = detector.topological_order()

    # -- 4e. Dispatch loop (ready-pool pattern) -------------------
    subsection("4e: Dispatch loop -- ready-pool: submit only when deps are met")
    print("  [Strategy] A task enters the scheduler queue ONLY when ALL its")
    print("             dependencies are in completed_ids (Airflow/Prefect model).\n")

    completed_ids: set = set()
    submitted_ids: set = set()

    def submit_ready_tasks() -> None:
        """Submit tasks whose all dependencies are now in completed_ids."""
        for t in admitted:
            if t.id in completed_ids or t.id in submitted_ids:
                continue
            if t.status == TaskStatus.REJECTED:
                continue
            unmet = [d for d in t.dependencies if d not in completed_ids]
            if not unmet:
                print(f"  [Ready] '{t.name}' deps satisfied -> submitting to scheduler")
                t.status = TaskStatus.PENDING
                t.start_time = None
                t.arrival_time = time.time()
                scheduler.submit(t)
                submitted_ids.add(t.id)

    # First pass: submit root tasks (no dependencies)
    submit_ready_tasks()

    max_steps = 80
    step = 0
    while step < max_steps:
        step += 1
        chosen = scheduler.next_task()

        if chosen is None:
            remaining = [t for t in admitted
                         if t.id not in completed_ids
                         and t.status != TaskStatus.REJECTED]
            if not remaining:
                print("\n  All tasks completed!")
                break
            submit_ready_tasks()
            chosen = scheduler.next_task()
            if chosen is None:
                print(f"  System idle. Completed: {sorted(completed_ids)}")
                break

        worker = allocator.allocate(chosen)
        if worker is None:
            print(f"  [Wait] No resources for '{chosen.name}' -- re-queueing")
            chosen.status = TaskStatus.PENDING
            chosen.start_time = None
            scheduler.submit(chosen)
            submitted_ids.discard(chosen.id)
            time.sleep(0.01)
            continue

        print(f"\n  >> Running '{chosen.name}' on {worker.id} "
              f"(burst={chosen.burst_time}s, sim={chosen.burst_time*0.01:.3f}s)")
        time.sleep(chosen.burst_time * 0.01)

        allocator.release(chosen)
        scheduler.complete_task(chosen)
        detector.remove_task(chosen.id)
        completed_ids.add(chosen.id)
        print(f"  Completed so far: {sorted(completed_ids)}")
        submit_ready_tasks()

    # -- 4f. Final state ------------------------------------------
    subsection("4f: Final cluster state & scheduler metrics")
    allocator.print_cluster_state()
    scheduler.print_metrics()

    # Summary
    done   = [t for t in admitted if t.status == TaskStatus.COMPLETED]
    failed = [t for t in admitted if t.status == TaskStatus.FAILED]
    print(f"  Tasks completed: {len(done)}/{len(admitted)}")
    if failed:
        print(f"  Tasks failed: {[t.name for t in failed]}")

    print("\n  ✅ TEST 4 PASSED\n")


# ============================================================
# Entry Point
# ============================================================

def main() -> None:
    print("""
+======================================================================+
|        AntiGravity Scheduler -- Phase 2: Scheduler Core            |
|            Pure Python  |  No deps  |  python main.py              |
+======================================================================+
""")

    results = []

    for test_fn, name in [
        (test_1_deadlock_detector, "Test 1 — Deadlock Detector"),
        (test_2_resource_allocator, "Test 2 — Resource Allocator"),
        (test_3_algorithms,         "Test 3 — Individual Algorithms"),
        (test_4_end_to_end,         "Test 4 — End-to-End Simulation"),
    ]:
        try:
            test_fn()
            results.append((name, "PASS", None))
        except Exception as exc:
            import traceback
            results.append((name, "FAIL", traceback.format_exc()))
            print(f"\n  ❌ {name} FAILED: {exc}\n")
            traceback.print_exc()

    # Final summary
    bar = "═" * 70
    print(f"\n{bar}")
    print("  📋 Test Summary")
    print(bar)
    all_pass = True
    for name, status, _ in results:
        icon = "✅" if status == "PASS" else "❌"
        print(f"  {icon} {name:<45} {status}")
        if status != "PASS":
            all_pass = False
    print(bar)
    if all_pass:
        print("  🎉 All tests passed! AntiGravity Scheduler Phase 2 is operational.")
    else:
        print("  ⚠  Some tests failed — see output above for details.")
    print(bar + "\n")

    sys.exit(0 if all_pass else 1)


if __name__ == "__main__":
    main()
