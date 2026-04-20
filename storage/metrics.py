import time
from typing import Dict

class CloudOSMetrics:
    """
    Simple metrics collector for CloudOS Scheduler.
    Mimics Prometheus metrics — counters and gauges.
    """
    def __init__(self):
        self.counters: Dict[str, int]   = {}
        self.gauges:   Dict[str, float] = {}
        self.start_time = time.time()

    def increment(self, name: str, value: int = 1):
        self.counters[name] = self.counters.get(name, 0) + value

    def set_gauge(self, name: str, value: float):
        self.gauges[name] = value

    def record_task_submitted(self, algorithm: str, priority: str):
        self.increment("tasks_submitted_total")
        self.increment(f"tasks_by_algorithm_{algorithm}")
        self.increment(f"tasks_by_priority_{priority}")

    def record_task_completed(self, duration: float):
        self.increment("tasks_completed_total")
        current_avg = self.gauges.get("avg_task_duration", 0)
        total = self.counters.get("tasks_completed_total", 1)
        self.gauges["avg_task_duration"] = (
            (current_avg * (total - 1) + duration) / total
        )

    def record_task_failed(self):
        self.increment("tasks_failed_total")

    def set_queue_depth(self, depth: int):
        self.set_gauge("queue_depth", depth)

    def set_active_workers(self, count: int):
        self.set_gauge("active_workers", count)

    def get_all(self) -> Dict:
        return {
            "uptime_seconds": round(time.time() - self.start_time, 2),
            "counters":       self.counters,
            "gauges":         self.gauges,
        }

    def prometheus_format(self) -> str:
        lines = []
        lines.append(f"# CloudOS Scheduler Metrics")
        lines.append(f"cloudos_uptime_seconds {time.time() - self.start_time:.2f}")
        for name, value in self.counters.items():
            lines.append(f"cloudos_{name} {value}")
        for name, value in self.gauges.items():
            lines.append(f"cloudos_{name} {value:.4f}")
        return "\n".join(lines)

metrics = CloudOSMetrics()
