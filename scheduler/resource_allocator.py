from dataclasses import dataclass, field
from typing import List, Optional, Dict

@dataclass
class Node:
    node_id:        str
    total_cpu:      float
    total_memory:   float
    used_cpu:       float = 0.0
    used_memory:    float = 0.0

    @property
    def available_cpu(self) -> float:
        return self.total_cpu - self.used_cpu

    @property
    def available_memory(self) -> float:
        return self.total_memory - self.used_memory

    @property
    def cpu_percent(self) -> float:
        return (self.used_cpu / self.total_cpu) * 100

    @property
    def memory_percent(self) -> float:
        return (self.used_memory / self.total_memory) * 100


@dataclass
class AllocationResult:
    success:   bool
    node_id:   Optional[str] = None
    message:   str = ""


class ResourceAllocator:
    """
    Bin packing based resource allocator.
    Mimics OS memory management — allocates
    CPU and memory to tasks across nodes.

    Strategies:
    - first_fit : first node that fits
    - best_fit  : node with least leftover space
    - worst_fit : node with most free space
    """

    def __init__(self):
        self.nodes: Dict[str, Node] = {}
        self.allocations: Dict[str, str] = {}

    def add_node(self, node_id: str, cpu: float, memory: float):
        self.nodes[node_id] = Node(
            node_id=node_id,
            total_cpu=cpu,
            total_memory=memory
        )
        print(f"[ALLOCATOR] Added node {node_id} CPU={cpu} MEM={memory}MB")

    def allocate(self, task_id: str, cpu: float,
                 memory: float, strategy: str = "best_fit") -> AllocationResult:

        candidates = [
            n for n in self.nodes.values()
            if n.available_cpu >= cpu and n.available_memory >= memory
        ]

        if not candidates:
            return AllocationResult(
                success=False,
                message=f"No node has enough resources for task {task_id}"
            )

        if strategy == "first_fit":
            chosen = candidates[0]
        elif strategy == "best_fit":
            chosen = min(candidates,
                key=lambda n: n.available_cpu - cpu + n.available_memory - memory)
        elif strategy == "worst_fit":
            chosen = max(candidates,
                key=lambda n: n.available_cpu + n.available_memory)
        else:
            chosen = candidates[0]

        chosen.used_cpu    += cpu
        chosen.used_memory += memory
        self.allocations[task_id] = chosen.node_id

        print(f"[ALLOCATOR] Allocated {task_id} → {chosen.node_id} "
              f"CPU={cpu} MEM={memory}MB")

        return AllocationResult(
            success=True,
            node_id=chosen.node_id,
            message=f"Task {task_id} allocated to {chosen.node_id}"
        )

    def deallocate(self, task_id: str, cpu: float, memory: float):
        if task_id not in self.allocations:
            return
        node_id = self.allocations.pop(task_id)
        node = self.nodes[node_id]
        node.used_cpu    -= cpu
        node.used_memory -= memory
        print(f"[ALLOCATOR] Deallocated {task_id} from {node_id}")

    def cluster_stats(self) -> Dict:
        return {
            node_id: {
                "cpu_used":        round(n.used_cpu, 2),
                "cpu_available":   round(n.available_cpu, 2),
                "cpu_percent":     round(n.cpu_percent, 1),
                "memory_used":     round(n.used_memory, 2),
                "memory_available":round(n.available_memory, 2),
                "memory_percent":  round(n.memory_percent, 1),
            }
            for node_id, n in self.nodes.items()
        }
