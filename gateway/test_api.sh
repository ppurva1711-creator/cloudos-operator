#!/usr/bin/env bash
# test_api.sh -- AntiGravity Scheduler Phase 3: API Integration Tests
# ====================================================================
# Run with: bash gateway/test_api.sh
# (Requires curl and a running server: uvicorn gateway.main:app --port 8000)
# ====================================================================

set -euo pipefail

BASE="http://localhost:8000"
PASS=0
FAIL=0

# ANSI colour helpers
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'

pass() { echo -e "${GREEN}[PASS]${NC} $1"; ((PASS++)); }
fail() { echo -e "${RED}[FAIL]${NC} $1"; ((FAIL++)); }
info() { echo -e "${YELLOW}[INFO]${NC} $1"; }

echo ""
echo "================================================="
echo "  AntiGravity Scheduler -- API Test Suite"
echo "================================================="
echo ""

# -----------------------------------------------------------------
# Helper: extract JSON field with python (avoids jq dependency)
# -----------------------------------------------------------------
json_get() {
    python3 -c "import sys,json; d=json.load(sys.stdin); print(d$1)" 2>/dev/null || echo ""
}

# -----------------------------------------------------------------
# TEST 1: Health check (no auth required)
# -----------------------------------------------------------------
info "TEST 1: Health check"
RESP=$(curl -sf "$BASE/health" || echo '{"status":"error"}')
STATUS=$(echo "$RESP" | json_get "['status']")
if [ "$STATUS" = "ok" ]; then
    pass "GET /health -> status=ok"
else
    fail "GET /health returned: $RESP"
fi

# -----------------------------------------------------------------
# TEST 2: Login as admin
# -----------------------------------------------------------------
info "TEST 2: Login as admin"
RESP=$(curl -sf -X POST "$BASE/auth/token" \
    -H "Content-Type: application/x-www-form-urlencoded" \
    -d "username=admin&password=adminpass")
ADMIN_TOKEN=$(echo "$RESP" | json_get "['access_token']")
if [ -n "$ADMIN_TOKEN" ] && [ "$ADMIN_TOKEN" != "None" ]; then
    pass "POST /auth/token (admin) -> got JWT"
else
    fail "POST /auth/token (admin) -> FAILED: $RESP"
    echo "Cannot continue without a token. Is the server running?"
    exit 1
fi

# -----------------------------------------------------------------
# TEST 3: Login as alice (user role)
# -----------------------------------------------------------------
info "TEST 3: Login as alice"
RESP=$(curl -sf -X POST "$BASE/auth/token" \
    -H "Content-Type: application/x-www-form-urlencoded" \
    -d "username=alice&password=alicepass")
ALICE_TOKEN=$(echo "$RESP" | json_get "['access_token']")
if [ -n "$ALICE_TOKEN" ] && [ "$ALICE_TOKEN" != "None" ]; then
    pass "POST /auth/token (alice) -> got JWT"
else
    fail "POST /auth/token (alice) -> FAILED: $RESP"
fi

# -----------------------------------------------------------------
# TEST 4: Login as bob (readonly role)
# -----------------------------------------------------------------
info "TEST 4: Login as bob (readonly)"
RESP=$(curl -sf -X POST "$BASE/auth/token" \
    -H "Content-Type: application/x-www-form-urlencoded" \
    -d "username=bob&password=bobpass")
BOB_TOKEN=$(echo "$RESP" | json_get "['access_token']")
if [ -n "$BOB_TOKEN" ] && [ "$BOB_TOKEN" != "None" ]; then
    pass "POST /auth/token (bob) -> got JWT"
else
    fail "POST /auth/token (bob) -> FAILED: $RESP"
fi

# -----------------------------------------------------------------
# TEST 5: Submit task 1 (no deps) as admin
# -----------------------------------------------------------------
info "TEST 5: Submit task 1 (fetch-data, burst=2.0, no deps)"
RESP=$(curl -sf -X POST "$BASE/tasks" \
    -H "Authorization: Bearer $ADMIN_TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"name":"fetch-data","burst_time":2.0,"priority":"HIGH","dependencies":[],"cpu_cores":1.0,"memory_mb":512}')
TASK1_ID=$(echo "$RESP" | json_get "['id']")
TASK1_STATUS=$(echo "$RESP" | json_get "['status']")
if [ -n "$TASK1_ID" ] && [ "$TASK1_ID" != "None" ]; then
    pass "POST /tasks -> id=$TASK1_ID status=$TASK1_STATUS"
else
    fail "POST /tasks (task1) -> FAILED: $RESP"
    TASK1_ID="unknown"
fi

# -----------------------------------------------------------------
# TEST 6: Submit task 2 with dependency on task 1
# -----------------------------------------------------------------
info "TEST 6: Submit task 2 (clean-data, depends on task 1)"
RESP=$(curl -sf -X POST "$BASE/tasks" \
    -H "Authorization: Bearer $ADMIN_TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"name\":\"clean-data\",\"burst_time\":3.0,\"priority\":\"NORMAL\",\"dependencies\":[\"$TASK1_ID\"],\"cpu_cores\":1.0,\"memory_mb\":512}")
TASK2_ID=$(echo "$RESP" | json_get "['id']")
if [ -n "$TASK2_ID" ] && [ "$TASK2_ID" != "None" ]; then
    pass "POST /tasks -> id=$TASK2_ID (depends on $TASK1_ID)"
else
    fail "POST /tasks (task2 with dep) -> FAILED: $RESP"
    TASK2_ID="unknown"
fi

# -----------------------------------------------------------------
# TEST 6.1: Deadlock detection (client-specified IDs)
# Create two tasks 'LOCK-A' and 'LOCK-B' with mutual dependencies
# -----------------------------------------------------------------
info "TEST 6.1: Deadlock check via explicit ids"
# Choose fixed 8-char ids so we can reference them deterministically.
A_ID="LOCKA123"
B_ID="LOCKB123"

# Submit A depending on B (B not yet created)
RESP=$(curl -sf -X POST "$BASE/tasks" \
    -H "Authorization: Bearer $ADMIN_TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"id\":\"$A_ID\",\"name\":\"deadlock-A\",\"burst_time\":1.0,\"priority\":\"NORMAL\",\"dependencies\":[\"$B_ID\"],\"cpu_cores\":0.5,\"memory_mb\":128}")
if [ $? -ne 0 ]; then
    fail "Initial deadlock-A submission failed: $RESP"
else
    pass "Submitted deadlock-A with id $A_ID depending on $B_ID"
fi

# Now submit B depending on A -- this should trigger a 409 conflict
HTTP_CODE=$(curl -so /dev/null -w "%{http_code}" -X POST "$BASE/tasks" \
    -H "Authorization: Bearer $ADMIN_TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"id\":\"$B_ID\",\"name\":\"deadlock-B\",\"burst_time\":1.0,\"priority\":\"NORMAL\",\"dependencies\":[\"$A_ID\"],\"cpu_cores\":0.5,\"memory_mb\":128}")
if [ "$HTTP_CODE" = "409" ]; then
    pass "Deadlock test: mutual dependency was rejected with 409"
else
    fail "Deadlock test failed: expected 409, got $HTTP_CODE"
fi

# -----------------------------------------------------------------
# TEST 7: Submit task 3 as alice (user role)
# -----------------------------------------------------------------
info "TEST 7: Alice submits a long-running task (burst=12s -> Round Robin)"
RESP=$(curl -sf -X POST "$BASE/tasks" \
    -H "Authorization: Bearer $ALICE_TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"name":"train-model","burst_time":12.0,"priority":"CRITICAL","dependencies":[],"cpu_cores":2.0,"memory_mb":2048}')
TASK3_ID=$(echo "$RESP" | json_get "['id']")
if [ -n "$TASK3_ID" ] && [ "$TASK3_ID" != "None" ]; then
    pass "POST /tasks (alice) -> id=$TASK3_ID"
else
    fail "POST /tasks (alice) -> FAILED: $RESP"
fi

# -----------------------------------------------------------------
# TEST 8: List all tasks
# -----------------------------------------------------------------
info "TEST 8: List all tasks"
RESP=$(curl -sf "$BASE/tasks" \
    -H "Authorization: Bearer $ADMIN_TOKEN")
COUNT=$(python3 -c "import sys,json; d=json.load(sys.stdin); print(len(d))" <<< "$RESP" 2>/dev/null || echo 0)
if [ "$COUNT" -gt 0 ] 2>/dev/null; then
    pass "GET /tasks -> $COUNT task(s) returned"
else
    fail "GET /tasks -> expected >0 tasks, got: $RESP"
fi

# -----------------------------------------------------------------
# TEST 9: Get single task by ID
# -----------------------------------------------------------------
info "TEST 9: Get task 1 by ID"
if [ "$TASK1_ID" != "unknown" ]; then
    RESP=$(curl -sf "$BASE/tasks/$TASK1_ID" \
        -H "Authorization: Bearer $ADMIN_TOKEN")
    GOT_ID=$(echo "$RESP" | json_get "['id']")
    if [ "$GOT_ID" = "$TASK1_ID" ]; then
        pass "GET /tasks/$TASK1_ID -> id matches"
    else
        fail "GET /tasks/$TASK1_ID -> id mismatch: $RESP"
    fi
else
    fail "Skipping get-task-by-id (task1 not created)"
fi

# -----------------------------------------------------------------
# TEST 10: 404 for non-existent task
# -----------------------------------------------------------------
info "TEST 10: 404 for non-existent task ID"
HTTP_CODE=$(curl -so /dev/null -w "%{http_code}" \
    -H "Authorization: Bearer $ADMIN_TOKEN" \
    "$BASE/tasks/does-not-exist-99")
if [ "$HTTP_CODE" = "404" ]; then
    pass "GET /tasks/does-not-exist -> 404 as expected"
else
    fail "GET /tasks/does-not-exist -> expected 404, got $HTTP_CODE"
fi

# -----------------------------------------------------------------
# TEST 11: Cluster state
# -----------------------------------------------------------------
info "TEST 11: Cluster state"
RESP=$(curl -sf "$BASE/cluster" \
    -H "Authorization: Bearer $ADMIN_TOKEN")
WORKER_COUNT=$(python3 -c "import sys,json; d=json.load(sys.stdin); print(len(d['workers']))" <<< "$RESP" 2>/dev/null || echo 0)
if [ "$WORKER_COUNT" -ge 3 ] 2>/dev/null; then
    pass "GET /cluster -> $WORKER_COUNT workers visible"
else
    fail "GET /cluster -> expected >=3 workers, got: $RESP"
fi

# -----------------------------------------------------------------
# TEST 12: Metrics (admin only)
# -----------------------------------------------------------------
info "TEST 12: Metrics (admin only)"
RESP=$(curl -sf "$BASE/metrics" \
    -H "Authorization: Bearer $ADMIN_TOKEN")
HAS_FIELD=$(python3 -c "import sys,json; d=json.load(sys.stdin); print('avg_wait_time' in d)" <<< "$RESP" 2>/dev/null || echo False)
if [ "$HAS_FIELD" = "True" ]; then
    pass "GET /metrics -> has avg_wait_time field"
else
    fail "GET /metrics -> unexpected response: $RESP"
fi

# -----------------------------------------------------------------
# TEST 13: Dependency graph
# -----------------------------------------------------------------
info "TEST 13: Dependency graph"
RESP=$(curl -sf "$BASE/graph" \
    -H "Authorization: Bearer $ADMIN_TOKEN")
HAS_FIELD=$(python3 -c "import sys,json; d=json.load(sys.stdin); print('graph' in d)" <<< "$RESP" 2>/dev/null || echo False)
if [ "$HAS_FIELD" = "True" ]; then
    pass "GET /graph -> has 'graph' field"
else
    fail "GET /graph -> unexpected response: $RESP"
fi

# -----------------------------------------------------------------
# TEST 14: 401 without token
# -----------------------------------------------------------------
info "TEST 14: 401 without auth token"
HTTP_CODE=$(curl -so /dev/null -w "%{http_code}" "$BASE/tasks")
if [ "$HTTP_CODE" = "401" ]; then
    pass "GET /tasks (no token) -> 401 as expected"
else
    fail "GET /tasks (no token) -> expected 401, got $HTTP_CODE"
fi

# -----------------------------------------------------------------
# TEST 15: 403 for bob (readonly) accessing admin endpoint
# -----------------------------------------------------------------
info "TEST 15: 403 for bob accessing DELETE /tasks (admin only)"
if [ "$TASK1_ID" != "unknown" ]; then
    HTTP_CODE=$(curl -so /dev/null -w "%{http_code}" \
        -X DELETE \
        -H "Authorization: Bearer $BOB_TOKEN" \
        "$BASE/tasks/$TASK1_ID")
    if [ "$HTTP_CODE" = "403" ]; then
        pass "DELETE /tasks/$TASK1_ID (bob=readonly) -> 403 as expected"
    else
        fail "DELETE /tasks/$TASK1_ID (bob) -> expected 403, got $HTTP_CODE"
    fi
else
    fail "Skipping 403 test (task1 not created)"
fi

# -----------------------------------------------------------------
# TEST 16: 403 for bob accessing admin /metrics
# -----------------------------------------------------------------
info "TEST 16: 403 for bob accessing GET /metrics"
HTTP_CODE=$(curl -so /dev/null -w "%{http_code}" \
    -H "Authorization: Bearer $BOB_TOKEN" \
    "$BASE/metrics")
if [ "$HTTP_CODE" = "403" ]; then
    pass "GET /metrics (bob=readonly) -> 403 as expected"
else
    fail "GET /metrics (bob) -> expected 403, got $HTTP_CODE"
fi

# -----------------------------------------------------------------
# TEST 17: Rate limit triggering (bob = 10 req/min limit)
# -----------------------------------------------------------------
info "TEST 17: Rate limit -- hammer /health 15x as bob (limit=10/min)"
RATE_LIMITED=0
for i in $(seq 1 15); do
    HTTP_CODE=$(curl -so /dev/null -w "%{http_code}" \
        -H "Authorization: Bearer $BOB_TOKEN" \
        "$BASE/rate-limits" 2>/dev/null || echo 000)
    if [ "$HTTP_CODE" = "429" ]; then
        RATE_LIMITED=$((RATE_LIMITED+1))
    fi
done
if [ "$RATE_LIMITED" -ge 1 ] 2>/dev/null; then
    pass "Rate limit triggered after 10 requests for bob -> got $RATE_LIMITED x 429"
else
    fail "Rate limit never triggered after 15 requests for bob (RATE_LIMITED=$RATE_LIMITED)"
fi

# -----------------------------------------------------------------
# TEST 18: Wrong password -> 401
# -----------------------------------------------------------------
info "TEST 18: Wrong password -> 401"
HTTP_CODE=$(curl -so /dev/null -w "%{http_code}" \
    -X POST "$BASE/auth/token" \
    -H "Content-Type: application/x-www-form-urlencoded" \
    -d "username=admin&password=wrongpassword")
if [ "$HTTP_CODE" = "401" ]; then
    pass "POST /auth/token (wrong pw) -> 401 as expected"
else
    fail "POST /auth/token (wrong pw) -> expected 401, got $HTTP_CODE"
fi

# -----------------------------------------------------------------
# TEST 19: Complete task 1 (admin) and verify COMPLETED status
# -----------------------------------------------------------------
info "TEST 19: Complete task 1 as admin"
if [ "$TASK1_ID" != "unknown" ]; then
    RESP=$(curl -sf -X POST "$BASE/tasks/$TASK1_ID/complete" \
        -H "Authorization: Bearer $ADMIN_TOKEN" \
        -H "Content-Type: application/json" \
        -d '{}')
    STATUS=$(echo "$RESP" | json_get "['status']")
    if [ "$STATUS" = "COMPLETED" ]; then
        pass "POST /tasks/$TASK1_ID/complete -> status=COMPLETED"
    else
        fail "POST /tasks/$TASK1_ID/complete -> unexpected status: $RESP"
    fi
else
    fail "Skipping complete-task test (task1 not created)"
fi

# -----------------------------------------------------------------
# TEST 20: Check rate-limits introspection endpoint as admin
# -----------------------------------------------------------------
info "TEST 20: Rate limit introspection for admin"
RESP=$(curl -sf "$BASE/rate-limits" \
    -H "Authorization: Bearer $ADMIN_TOKEN")
ROLE=$(echo "$RESP" | json_get "['role']")
if [ "$ROLE" = "admin" ]; then
    pass "GET /rate-limits -> role=admin confirmed"
else
    fail "GET /rate-limits -> unexpected: $RESP"
fi

# -----------------------------------------------------------------
# Summary
# -----------------------------------------------------------------
echo ""
echo "================================================="
echo "  Test Summary"
echo "================================================="
echo -e "  ${GREEN}PASS: $PASS${NC}"
echo -e "  ${RED}FAIL: $FAIL${NC}"
TOTAL=$((PASS + FAIL))
echo "  TOTAL: $TOTAL"
echo "================================================="
if [ "$FAIL" -eq 0 ]; then
    echo -e "  ${GREEN}All tests passed!${NC}"
    exit 0
else
    echo -e "  ${RED}$FAIL test(s) failed.${NC}"
    exit 1
fi
