import networkx as nx
from typing import List, Dict, Optional

class DeadlockDetector:
    """
    Detects deadlocks in task dependency graph.
    Uses DFS cycle detection — same concept as OS
    resource allocation graph deadlock detection.
    
    Example deadlock:
    Task A depends on Task B
    Task B depends on Task C  
    Task C depends on Task A  ← CYCLE = DEADLOCK!
    """

    def __init__(self):
        self.graph = nx.DiGraph()

    def add_task(self, task_id: str, depends_on: List[str]):
        self.graph.add_node(task_id)
        for dep in depends_on:
            self.graph.add_node(dep)
            self.graph.add_edge(task_id, dep)

    def remove_task(self, task_id: str):
        if self.graph.has_node(task_id):
            self.graph.remove_node(task_id)

    def has_deadlock(self) -> bool:
        try:
            cycle = nx.find_cycle(self.graph, orientation="original")
            return True
        except nx.NetworkXNoCycle:
            return False

    def get_cycle(self) -> Optional[List[str]]:
        try:
            cycle = nx.find_cycle(self.graph, orientation="original")
            return [edge[0] for edge in cycle]
        except nx.NetworkXNoCycle:
            return None

    def get_execution_order(self) -> Optional[List[str]]:
        """
        Topological sort — returns safe execution order.
        Returns None if deadlock exists.
        """
        if self.has_deadlock():
            return None
        return list(reversed(list(nx.topological_sort(self.graph))))

    def get_ready_tasks(self, completed: List[str]) -> List[str]:
        """
        Returns tasks whose all dependencies are completed.
        Mimics OS scheduler picking runnable processes.
        """
        ready = []
        for node in self.graph.nodes:
            if node in completed:
                continue
            deps = list(self.graph.successors(node))
            if all(dep in completed for dep in deps):
                ready.append(node)
        return ready

    def visualize(self) -> Dict:
        return {
            "nodes": list(self.graph.nodes),
            "edges": [{"from": u, "to": v} for u, v in self.graph.edges],
            "has_deadlock": self.has_deadlock(),
            "cycle": self.get_cycle(),
            "execution_order": self.get_execution_order()
        }
