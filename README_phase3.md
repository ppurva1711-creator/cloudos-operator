# AntiGravity Scheduler — Phase 3: API Gateway

## Quick Start

### 1. Install dependencies
```bash
# From the project root (CLOUDTASKSCHEDUKLER/)
pip install -r requirements.txt
```

### 2. Start the gateway
```bash
# From the project root
uvicorn gateway.main:app --reload --port 8000
```

> **Note:** For testing purposes the `/tasks` endpoint accepts an optional
> `id` field in the JSON body. This allows you to craft deterministic
> dependency graphs (e.g. to trigger the deadlock detector). Production
> clients should omit it; the server will generate a random 8‑char UUID.

The server starts at **http://localhost:8000**  
Interactive API docs: **http://localhost:8000/docs**

---

## Project Structure

```
CLOUDTASKSCHEDUKLER/
├── scheduler/               ← Phase 2: Scheduler Core
│   ├── models.py
│   ├── priority_queue.py
│   ├── deadlock_detector.py
│   ├── resource_allocator.py
│   └── main.py
├── gateway/                 ← Phase 3: API Gateway
│   ├── __init__.py
│   ├── auth.py              ← JWT + bcrypt + RBAC
│   ├── rate_limit.py        ← Token bucket rate limiter
│   ├── main.py              ← FastAPI app + all routes
│   └── test_api.sh          ← curl integration tests
├── requirements.txt
└── README_phase3.md
```

---

## Test Users

| Username | Password    | Role     | Rate Limit  |
|----------|-------------|----------|-------------|
| admin    | adminpass   | admin    | 200 req/min |
| alice    | alicepass   | user     | 60 req/min  |
| bob      | bobpass     | readonly | 10 req/min  |

---

## Example curl Commands

### 1. Login and get a token
```bash
TOKEN=$(curl -s -X POST http://localhost:8000/auth/token \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "username=admin&password=adminpass" | python3 -c "import sys,json; print(json.load(sys.stdin)['access_token'])")
echo "Token: $TOKEN"
```

### 2. Submit a task
```bash
curl -s -X POST http://localhost:8000/tasks \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"fetch-data","burst_time":2.0,"priority":"HIGH","cpu_cores":1.0,"memory_mb":512}' \
  | python3 -m json.tool
```

### 3. List all tasks
```bash
curl -s http://localhost:8000/tasks \
  -H "Authorization: Bearer $TOKEN" | python3 -m json.tool
```

### 4. Get cluster state
```bash
curl -s http://localhost:8000/cluster \
  -H "Authorization: Bearer $TOKEN" | python3 -m json.tool
```

### 5. Get scheduler metrics (admin only)
```bash
curl -s http://localhost:8000/metrics \
  -H "Authorization: Bearer $TOKEN" | python3 -m json.tool
```

### 6. Get dependency graph
```bash
curl -s http://localhost:8000/graph \
  -H "Authorization: Bearer $TOKEN" | python3 -m json.tool
```

### 7. Mark a task complete
```bash
curl -s -X POST http://localhost:8000/tasks/{task_id}/complete \
  -H "Authorization: Bearer $TOKEN" | python3 -m json.tool
```

---

## Run Bash Tests
```bash
# Start the server first, then in another terminal:
bash gateway/test_api.sh

# The script exercises every endpoint, including a deliberate deadlock
# scenario using client-supplied task IDs. Look for a 409 conflict message
# in the output to confirm the detector is working.
```
---

## API Reference

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| POST | `/auth/token` | None | Login, get JWT |
| GET | `/health` | None | System health |
| POST | `/tasks` | user/admin | Submit task |
| GET | `/tasks` | any | List tasks (`?status=pending&limit=20&offset=0`) |
| GET | `/tasks/{id}` | any | Get task details |
| DELETE | `/tasks/{id}` | admin | Cancel pending task |
| POST | `/tasks/{id}/complete` | user/admin | Mark task done |
| GET | `/cluster` | any | Worker pool state |
| GET | `/metrics` | admin | Scheduling metrics |
| GET | `/graph` | any | Dependency graph + topo order |
| GET | `/rate-limits` | any | Your current rate limit status |

---

## Key Design Decisions

- **No database** — all state lives in Phase 2's in-memory `Scheduler`, `DeadlockDetector`, and `ResourceAllocator` objects
- **JWT auth** — 30-minute tokens signed with HS256; role embedded in payload claim
- **Token bucket** — built from scratch; per-user buckets with role-based capacity (200/60/10 req/min)
- **Deadlock prevention** — `can_add()` dry-run before every `POST /tasks`; returns HTTP 409 with cycle path on conflict
- **Automatic routing** — `Scheduler.submit()` auto-selects RR/SJF/MLFQ based on burst time and priority
