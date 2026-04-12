const BASE_URL = "https://supreme-adventure-x5gr5qj96xxp2pp9q-8000.app.github.dev"

export const api = {
  health: () =>
    fetch(`${BASE_URL}/health`).then(r => r.json()),

  submitTask: (task) =>
    fetch(`${BASE_URL}/tasks/submit`, {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify(task)
    }).then(r => r.json()),

  getStats: () =>
    fetch(`${BASE_URL}/scheduler/stats`).then(r => r.json()),

  getClusterStats: () =>
    fetch(`${BASE_URL}/cluster/stats`).then(r => r.json()),

  getNextTask: () =>
    fetch(`${BASE_URL}/tasks/next`).then(r => r.json()),

  getExecutionOrder: () =>
    fetch(`${BASE_URL}/scheduler/execution-order`).then(r => r.json()),

  changeAlgorithm: (algorithm) =>
    fetch(`${BASE_URL}/scheduler/algorithm`, {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({algorithm})
    }).then(r => r.json()),
}
