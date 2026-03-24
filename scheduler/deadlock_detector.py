"""
deadlock_detector.py — AntiGravity Scheduler: Deadlock Detection Engine
========================================================================

OS Analogy: Resource Allocation Graph (RAG) & Wait-For Graph (WFG)
-------------------------------------------------------------------
Deadlock is one of the four classic problems in concurrent systems. Coffman et al.
(1971) identified four necessary and SUFFICIENT conditions for deadlock:

  1. Mutual Exclusion   — A resource can only be held by one process at a time.
  2. Hold and Wait      — A process holds resources while waiting for others.
  3. No Preemption      — Resources cannot be forcibly taken; they must be released.
  4. Circular Wait      — A cycle exists in the Wait-For Graph (WFG).

This module detects Condition #4 (Circular Wait) using a directed dependency graph:
  - Node: task ID
  - Edge A → B: "task A depends on task B" (A cannot run until B completes)
  - A cycle in this graph = circular wait = DEADLOCK

Real-world analogues:
  - Linux kernel uses a lock dependency graph (lockdep) that tracks lock acquisition
    order and detects if any order creates a cycle (potential deadlock).
  - Database systems (PostgreSQL, InnoDB) run cycle detection on transaction
    wait-for graphs periodically and abort the youngest transaction (victim selection).
  - Apache Airflow's DAG scheduler refuses to create a DAG with cycles.

Detection algorithm: DFS with WHITE/GREY/BLACK (tri-colour) marking.
  WHITE = not visited, GREY = in current DFS stack (being explored), BLACK = done.
  A back edge (GREY → GREY) indicates a cycle — O(V+E) time, O(V) space.

Topological ordering (Kahn's algorithm):
  Used by Airflow/Prefect to find the valid execution order of a DAG.
  BFS-based: repeatedly remove nodes with in-degree 0 (no pending dependencies).
"""

from __future__ import annotations

import copy
from collections import deque
from typing import Dict, List, Optional, Set, Tuple

# Attempt to import networkx for optional validation.
# The manual DFS implementation works without it.
try:
    import networkx as nx
    _HAS_NX = True
except ImportError:
    _HAS_NX = False


# ---------------------------------------------------------------------------
# Tri-colour DFS node states (mirrors lockdep / academic graph colouring)
# ---------------------------------------------------------------------------
WHITE = 0   # Not yet visited
GREY  = 1   # Currently in DFS recursion stack (open / being explored)
BLACK = 2   # Fully explored (all descendants processed)


class DeadlockDetector:
    """
    Dependency graph manager and deadlock detector.

    Coffman Condition #4 — Circular Wait:
      "A set of processes {P1, P2, …, Pn} exists such that P1 is waiting for P2,
       P2 is waiting for P3, …, Pn is waiting for P1."

    This class builds a Wait-For Graph (WFG) from task dependencies and uses
    DFS cycle detection to identify circular waits before they materialise.
    The `can_add` dry-run prevents deadlocks from being introduced (proactive).
    The `check_current_graph` scan catches externally introduced cycles (reactive).
    """

    def __init__(self) -> None:
        # Adjacency list: graph[task_id] = [list of task_ids it depends on]
        # Edge A → B means "A waits for B" (B must complete before A can run).
        self.graph: Dict[str, List[str]] = {}

    # ------------------------------------------------------------------
    def add_task(self, task_id: str, dependencies: List[str]) -> None:
        """
        Register a task and its dependencies in the live graph.

        OS Analogy: When a process calls wait() or acquires a mutex that another
        process holds, the kernel adds an edge in the WFG. In Linux's lockdep,
        lock_acquire() records the edge and runs the cycle check.

        IMPORTANT: Always call can_add() BEFORE add_task() to ensure no cycle
        is introduced. This mirrors Banker's Algorithm's 'request validation'
        step in deadlock avoidance systems.
        """
        self.graph[task_id] = list(dependencies)
        # Ensure dependency nodes exist in the graph (even if they have no deps of their own).
        for dep in dependencies:
            if dep not in self.graph:
                self.graph[dep] = []
        print(f"[Deadlock] Registered task '{task_id}' with deps={dependencies}")

    # ------------------------------------------------------------------
    def remove_task(self, task_id: str) -> None:
        """
        Remove a completed task from the graph and clean up all references.

        OS Analogy: When a process exits (do_exit()), the kernel removes all its
        held locks and wakes up processes waiting on them. In the WFG, removing node
        P removes all edges P→X and Y→P, potentially breaking cycles.

        Airflow does the same when a task_instance reaches the DONE state —
        it removes it from the dependency graph so downstream tasks become READY.
        """
        # Remove the task's own node.
        removed = self.graph.pop(task_id, None)
        if removed is not None:
            print(f"[Deadlock] Removed task '{task_id}' from graph")

        # Remove any remaining references to this task_id in other tasks' dependency lists.
        for tid in self.graph:
            before = len(self.graph[tid])
            self.graph[tid] = [d for d in self.graph[tid] if d != task_id]
            if len(self.graph[tid]) < before:
                print(f"[Deadlock] Cleaned reference to '{task_id}' from '{tid}'s deps")

    # ------------------------------------------------------------------
    def can_add(self, task_id: str, dependencies: List[str]) -> Tuple[bool, Optional[List[str]]]:
        """
        Dry-run: test if adding this task+deps would introduce a cycle.

        OS Analogy: Banker's Algorithm 'request' phase — before granting a resource
        request, simulate the allocation on a COPY of the state and check if the
        resulting state is 'safe' (no cycle). If safe, allow; otherwise deny.

        We use copy.deepcopy() to avoid modifying the live graph during the check.
        This is the 'optimistic concurrency control' pattern used in databases:
        tentatively apply, then validate, then commit or rollback.

        Returns:
          (True,  None)       — safe to add
          (False, cycle_path) — would introduce a deadlock; cycle_path shows the cycle
        """
        # Create a deep copy — this is our 'hypothetical' state.
        test_graph: Dict[str, List[str]] = copy.deepcopy(self.graph)
        test_graph[task_id] = list(dependencies)
        for dep in dependencies:
            if dep not in test_graph:
                test_graph[dep] = []

        cycle = self._find_cycle(test_graph)
        if cycle:
            print(f"[Deadlock] ⚠ can_add('{task_id}') → REJECTED: would create cycle {cycle}")
            return (False, cycle)

        print(f"[Deadlock] ✓ can_add('{task_id}') → SAFE (no cycle detected)")
        return (True, None)

    # ------------------------------------------------------------------
    def check_current_graph(self) -> Optional[List[str]]:
        """
        Scan the live graph for existing cycles (reactive deadlock detection).

        OS Analogy: Linux's lockdep runs this check on every lock acquisition.
        Database systems (PostgreSQL) run a deadlock detector thread that scans the
        WFG periodically (default every 1 second) and kills victim transactions.

        This method should be called as a periodic background daemon (e.g., every 30s
        via a threading.Timer or asyncio task) to catch cycles introduced by
        external modifications or race conditions.

        Returns the cycle path if found, None if graph is clean.
        """
        cycle = self._find_cycle(self.graph)
        if cycle:
            print(f"[Deadlock] 🚨 DEADLOCK DETECTED in live graph! Cycle: {' → '.join(cycle)}")
        else:
            print(f"[Deadlock] ✅ Live graph is cycle-free ({len(self.graph)} nodes)")
        return cycle

    # ------------------------------------------------------------------
    def _find_cycle(self, graph: Dict[str, List[str]]) -> Optional[List[str]]:
        """
        Core cycle detection: DFS with WHITE/GREY/BLACK tri-colour marking.

        Algorithm (from CLRS §22.3 / Tarjan 1972):
          - WHITE (0): node not yet reached by DFS
          - GREY  (1): node is on the current DFS recursion stack (open frontier)
          - BLACK (2): node is fully explored; all descendants checked

        A BACK EDGE is detected when DFS encounters a GREY node — meaning we reached
        a node that is already in our current recursion path → CYCLE.

        Time Complexity:  O(V + E)  — each node and edge visited at most twice
        Space Complexity: O(V)      — for the colour map and recursion stack

        This is the algorithm used in:
          - Linux lockdep (lock dependency cycle checker)
          - Kahn's algorithm preprocessing (Kahn's BFS is an alternative)
          - Airflow's DAG validation (uses networkx.is_directed_acyclic_graph internally,
            which runs Tarjan's SCC under the hood)

        Returns:
          List like ["A","B","C","A"] showing the cycle, or None if acyclic.
        """
        colour: Dict[str, int] = {node: WHITE for node in graph}
        parent: Dict[str, Optional[str]] = {node: None for node in graph}

        def dfs(node: str) -> Optional[List[str]]:
            colour[node] = GREY  # Mark as 'in progress' (on recursion stack)

            for neighbour in graph.get(node, []):
                # Neighbour not in colour map — it's an external dependency node.
                # Add it as a BLACK (fully explored, no outgoing deps from it).
                if neighbour not in colour:
                    colour[neighbour] = BLACK
                    continue

                if colour[neighbour] == GREY:
                    # BACK EDGE FOUND — reconstruct the cycle path.
                    # Walk back via parent pointers until we reach `neighbour` again.
                    cycle_path = [neighbour, node]
                    current = parent[node]
                    while current is not None and current != neighbour:
                        cycle_path.append(current)
                        current = parent[current]
                    cycle_path.append(neighbour)
                    cycle_path.reverse()
                    return cycle_path

                if colour[neighbour] == WHITE:
                    parent[neighbour] = node
                    result = dfs(neighbour)
                    if result:
                        return result

            colour[node] = BLACK  # Fully explored — mark as done
            return None

        # Run DFS from every unvisited node (handles disconnected graph components).
        for node in list(graph.keys()):
            if colour.get(node, WHITE) == WHITE:
                result = dfs(node)
                if result:
                    return result

        # Optional: validate using networkx if available.
        if _HAS_NX:
            G = nx.DiGraph()
            for node, deps in graph.items():
                G.add_node(node)
                for dep in deps:
                    G.add_edge(node, dep)
            nx_has_cycle = not nx.is_directed_acyclic_graph(G)
            # Our manual result should agree with networkx.
            if nx_has_cycle:
                print(f"[Deadlock] [networkx] Confirmed: cycle exists in graph")
            else:
                print(f"[Deadlock] [networkx] Confirmed: graph is a DAG")

        return None

    # ------------------------------------------------------------------
    def get_ready_tasks(self, all_task_ids: List[str], completed_ids: Set[str]) -> List[str]:
        """
        Return tasks whose ALL dependencies are satisfied (in completed_ids).

        OS Analogy: This is exactly how Apache Airflow and Prefect determine which
        task_instances to schedule next. Airflow's TaskInstance.are_dependencies_met()
        checks each upstream task's state. Here we do the same with set intersection.

        In OS terms, this is analogous to finding threads whose waited mutex / condition
        variable has been signalled — they can now enter the READY queue.

        Algorithm:
          For each task T in all_task_ids:
            if set(deps(T)) ⊆ completed_ids → T is READY
          O(V + E) where V = tasks, E = total dependency edges
        """
        ready = []
        for task_id in all_task_ids:
            if task_id in completed_ids:
                continue  # already done
            deps = set(self.graph.get(task_id, []))
            if deps.issubset(completed_ids):
                ready.append(task_id)

        print(f"[Deadlock] Ready tasks (deps satisfied): {ready}")
        return ready

    # ------------------------------------------------------------------
    def topological_order(self) -> Optional[List[str]]:
        """
        Compute a valid task execution order using Kahn's Algorithm (BFS).

        Kahn's Algorithm (1962) — the standard BFS-based topological sort:
          1. Compute in-degree for every node (number of unsatisfied dependencies).
          2. Enqueue all nodes with in-degree 0 (no dependencies → immediately runnable).
          3. While queue is not empty:
               - Dequeue node N, add to result.
               - For each node M that depends on N: decrement M's in-degree.
               - If M's in-degree reaches 0: enqueue M.
          4. If result has fewer nodes than the graph → cycle exists.

        OS Analogy: Makefile dependency resolution (`make -j`), systemd unit ordering
        (Before= / After= directives), and Airflow's execution plan all use topological
        sort to determine the valid start order.

        Returns:
          Ordered list of task IDs if DAG is valid, None if a cycle exists.
        """
        # Build in-degree map: count how many tasks depend on each task.
        # Note: edge A→B means A depends on B, so B has an out-edge to A's "waiter".
        # For execution order, we want nodes with no UNSATISFIED predecessors first.
        # Here edges go FROM depender TO dependency, so in-degree of a node X =
        # number of tasks that list X as a dependency.
        # For Kahn's we need: which nodes have no incoming dependency edges?
        # A node with graph[node] = [] has no outgoing dependency edges → no dependencies
        # → can run immediately → starts with in-degree 0 in the execution perspective.

        # Re-model: execution_deps[task] = set of tasks it WAITS FOR (must complete first)
        execution_deps: Dict[str, Set[str]] = {}
        all_nodes: Set[str] = set(self.graph.keys())

        for node in all_nodes:
            execution_deps[node] = set(self.graph[node])
            # Add dependency nodes even if they have no further deps.
            for dep in self.graph[node]:
                all_nodes.add(dep)
                if dep not in execution_deps:
                    execution_deps[dep] = set()

        # BFS queue: tasks with no remaining dependencies
        queue: deque[str] = deque()
        for node in sorted(all_nodes):  # sorted for deterministic output
            if not execution_deps[node]:
                queue.append(node)

        topo_order: List[str] = []

        while queue:
            # Dequeue a node with no unsatisfied deps → it can run now.
            node = queue.popleft()
            topo_order.append(node)

            # Remove this node from the dependency sets of all other nodes.
            # If a node's dep set becomes empty, enqueue it.
            for other in sorted(all_nodes):
                if node in execution_deps[other]:
                    execution_deps[other].discard(node)
                    if not execution_deps[other] and other not in topo_order:
                        queue.append(other)

        if len(topo_order) != len(all_nodes):
            # Not all nodes were reachable → cycle exists → no valid ordering.
            print(f"[Deadlock] ⚠ Topological sort failed — cycle detected! "
                  f"Processed {len(topo_order)}/{len(all_nodes)} nodes")
            return None

        print(f"[Deadlock] Topological order: {' → '.join(topo_order)}")
        return topo_order

    # ------------------------------------------------------------------
    def print_graph(self) -> None:
        """Print the current dependency graph in human-readable format."""
        print("\n[Deadlock] Dependency Graph (A → B means A waits for B):")
        if not self.graph:
            print("  [empty graph]")
            return
        for node, deps in sorted(self.graph.items()):
            dep_str = ", ".join(deps) if deps else "(none)"
            print(f"  {node} → [{dep_str}]")
        print()
