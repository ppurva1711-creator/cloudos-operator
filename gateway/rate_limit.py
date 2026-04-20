# -*- coding: utf-8 -*-
"""
rate_limit.py -- AntiGravity Scheduler: Token Bucket Rate Limiter
==================================================================

OS Analogy: Traffic Shaping & Network QoS (Quality of Service)
---------------------------------------------------------------
Rate limiting is the API-layer equivalent of kernel network traffic shaping:

  - Linux `tc` (Traffic Control) implements token bucket (TBF qdisc) in the
    kernel's network stack to enforce per-interface bandwidth limits.
  - The iptables `limit` module uses a token bucket to rate-limit firewall rule
    matches (e.g., `--limit 10/s` for SSH brute-force protection).
  - This module brings the same algorithm to HTTP request scheduling.

Three common rate-limiting algorithms compared:
-----------------------------------------------

1. TOKEN BUCKET (used here):
   - A bucket holds up to `capacity` tokens.
   - Tokens refill at `refill_rate` tokens/second continuously.
   - Each request consumes 1 token. If empty -> reject.
   - Allows short BURSTS up to `capacity` while enforcing a long-term average rate.
   - Analogy: pcm (Packet Credit Meter) in telecom / Linux TBF qdisc.
   - Best for: APIs that want to allow burst traffic from legitimate users.

2. LEAKY BUCKET:
   - Requests enter a fixed-capacity queue; they drain at a constant rate.
   - If queue is full -> reject (overflow). No burst accommodation.
   - Analogy: A leaky garden bucket -- water drips out at a constant rate
     regardless of how fast you pour it in.
   - Best for: Enforcing strict, smooth output rate (e.g., financial transaction APIs).

3. FIXED WINDOW COUNTER:
   - Count requests in time windows (e.g., 60s slots).
   - Reset counter at window boundary.
   - Simple but has "boundary burst" flaw: 100 reqs at 00:59 + 100 at 01:00 = 200 in 2s.
   - Analogy: Linux `ulimit -u` (max processes per user) -- a hard cap, not smooth.
   - Best for: Rough rate limiting where simplicity > precision.

TOKEN BUCKET wins for API rate limiting because it smoothly allows burst traffic
(a user opening their dashboard triggers several parallel requests) while still
enforcing the long-term average -- the fairest trade-off for human users.
"""

from __future__ import annotations

import time
from typing import Dict, Tuple

from fastapi import Depends, HTTPException, Request, status

from gateway.auth import UserInDB, get_current_user

# ---------------------------------------------------------------------------
# Per-role rate limits (requests per minute)
# ---------------------------------------------------------------------------
ROLE_LIMITS: Dict[str, int] = {
    "admin":    200,   # Operators need headroom for management tasks
    "user":     60,    # Standard API consumers: 1 req/second average
    "readonly": 10,    # Monitoring/observer clients: low-volume polling
}


# ---------------------------------------------------------------------------
# TokenBucket -- the core algorithm
# ---------------------------------------------------------------------------

class TokenBucket:
    """
    A token bucket rate limiter implemented from scratch.

    Algorithm (continuous refill model):
      capacity     = maximum tokens the bucket can hold (burst size)
      refill_rate  = tokens added per second (sustained throughput)
      tokens       = current token count (float for sub-second precision)

    On each consume() call:
      1. Calculate elapsed time since last refill.
      2. Add (elapsed * refill_rate) tokens, capped at capacity.
      3. If tokens >= requested amount: deduct and return True (allowed).
      4. Otherwise: return False (rate-limited -- HTTP 429).

    This is mathematically equivalent to Linux's TBF (Token Bucket Filter)
    qdisc: each packet consumes tokens from the bucket; the bucket refills at
    rate r with burst limit b. The kernel calculates token count lazily
    (only when a packet arrives) exactly as we do here.
    """

    def __init__(self, capacity: int, refill_rate: float) -> None:
        """
        capacity:    max tokens in bucket (= allowed burst size)
        refill_rate: tokens added per second (= sustained req/s)

        For 60 req/min: capacity=60, refill_rate=1.0 (1 token/second).
        For 200 req/min: capacity=200, refill_rate=3.33 (200/60 tokens/second).
        """
        self.capacity:     float = float(capacity)
        self.refill_rate:  float = refill_rate       # tokens per second
        self._tokens:      float = float(capacity)   # start full (new clients get a burst)
        self._last_refill: float = time.monotonic()  # monotonic clock: unaffected by NTP

    def _refill(self) -> None:
        """
        Lazily refill the bucket based on elapsed wall time.

        We use time.monotonic() instead of time.time() because:
          - monotonic() never goes backwards (immune to NTP adjustments)
          - time()     CAN go backwards when system clock is synced
          Backwards time would give negative tokens -- a subtle bug.

        OS Analogy: Linux's ktime_get() for monotonic kernel time,
        used in networking subsystems precisely for this reason.
        """
        now = time.monotonic()
        elapsed = now - self._last_refill
        self._tokens = min(
            self.capacity,
            self._tokens + elapsed * self.refill_rate
        )
        self._last_refill = now

    def consume(self, tokens: int = 1) -> bool:
        """
        Try to consume `tokens` from the bucket.

        Returns True if the request is allowed (tokens deducted).
        Returns False if the bucket is empty (caller should return HTTP 429).

        Thread-safety note: In production with async workers, this should use
        an asyncio.Lock or a Redis atomic DECR. For single-process uvicorn
        (default) with Python's GIL, this is safe.
        """
        self._refill()
        if self._tokens >= tokens:
            self._tokens -= tokens
            return True
        return False   # Bucket empty -- request rejected

    @property
    def remaining(self) -> int:
        """
        Current token count (floor to int for HTTP header).
        X-RateLimit-Remaining header value.
        """
        self._refill()
        return int(self._tokens)

    @property
    def reset_in(self) -> float:
        """
        Seconds until the bucket is completely full again.
        X-RateLimit-Reset header value (seconds from now).

        Formula: (capacity - current_tokens) / refill_rate
        """
        self._refill()
        deficit = self.capacity - self._tokens
        if deficit <= 0:
            return 0.0
        return round(deficit / self.refill_rate, 2)


# ---------------------------------------------------------------------------
# RateLimiter -- manages one bucket per client
# ---------------------------------------------------------------------------

class RateLimiter:
    """
    Multi-client rate limiter: one TokenBucket per (client_id, role) pair.

    The client_id is the username from the JWT (authenticated clients) or
    the IP address (for unauthenticated endpoints, if added later).

    OS Analogy: Linux cgroups blkio/cpu subsystems track per-cgroup resource
    usage. Here we track per-user API consumption with individual token buckets,
    analogous to per-process CPU quotas enforced by cgroup.cpu.cfs_quota_us.
    """

    def __init__(self) -> None:
        # Dict[client_id -> TokenBucket]
        # In production: use Redis with DECR + TTL for distributed rate limiting.
        self._buckets: Dict[str, TokenBucket] = {}

    def _make_bucket(self, role: str) -> TokenBucket:
        """
        Create a bucket sized for the given role's limit.
        capacity = limit (max burst = full minute allowance)
        refill_rate = limit / 60 (sustain the per-minute average)
        """
        limit = ROLE_LIMITS.get(role, 10)
        return TokenBucket(capacity=limit, refill_rate=limit / 60.0)

    def get_bucket(self, client_id: str, role: str = "readonly") -> TokenBucket:
        """
        Get or create the token bucket for this client.
        New clients start with a full bucket (OS: new process starts with full rlimits).
        The role is only used when creating a new bucket.
        """
        if client_id not in self._buckets:
            self._buckets[client_id] = self._make_bucket(role)
            print(f"[RATE] New rate-limit bucket for '{client_id}' "
                  f"(role={role}, limit={ROLE_LIMITS.get(role, 10)} req/min)")
        return self._buckets[client_id]

    def check(self, client_id: str, role: str = "readonly") -> Tuple[bool, int, float]:
        """
        Check if this client is within rate limits and consume one token.

        Returns:
          (allowed: bool, remaining: int, reset_in: float)

        The returned values map directly to standard Rate Limit HTTP headers:
          X-RateLimit-Limit     : ROLE_LIMITS[role]
          X-RateLimit-Remaining : remaining
          X-RateLimit-Reset     : reset_in (seconds)

        These headers follow the IETF draft for HTTP Rate Limit Headers
        (draft-polli-ratelimit-headers-06).
        """
        bucket = self.get_bucket(client_id, role)
        allowed = bucket.consume(1)
        remaining = bucket.remaining
        reset_in = bucket.reset_in

        if not allowed:
            print(f"[RATE] RATE LIMITED '{client_id}' (role={role}) | "
                  f"remaining={remaining} | reset_in={reset_in:.1f}s")
        return (allowed, remaining, reset_in)


# ---------------------------------------------------------------------------
# Singleton rate limiter instance (shared across all requests in the process)
# ---------------------------------------------------------------------------
_rate_limiter = RateLimiter()


# ---------------------------------------------------------------------------
# FastAPI dependency
# ---------------------------------------------------------------------------

async def rate_limit_dependency(
    request: Request,
    current_user: UserInDB = Depends(get_current_user),
) -> UserInDB:
    print(f"[DEBUG] rate_limit_dependency called for {current_user.username}")
    """
    FastAPI dependency that enforces rate limiting for authenticated routes.

    Called automatically before protected route handlers via:
        @app.post("/tasks", dependencies=[Depends(rate_limit_dependency)])

    OS Analogy: Linux's cgroup.cpu.cfs_quota_us -- before a process can consume
    CPU time, the scheduler checks if its cgroup still has quota remaining.
    If the quota is exhausted, the process is throttled (put to sleep until
    the next period). Here, throttled requests get HTTP 429 instead of sleep.

    HTTP 429 Too Many Requests (RFC 6585):
      - Rate limit headers inform the client how long to back off.
      - The Retry-After header (seconds) lets clients implement smart retry.
    """
    client_id = current_user.username
    role = current_user.role
    allowed, remaining, reset_in = _rate_limiter.check(client_id, role)
    limit = ROLE_LIMITS.get(role, 10)

    if not allowed:
        raise HTTPException(
            status_code=status.HTTP_429_TOO_MANY_REQUESTS,
            detail=f"Rate limit exceeded. Your limit: {limit} req/min. "
                   f"Retry after {reset_in:.1f}s.",
            headers={
                "X-RateLimit-Limit":     str(limit),
                "X-RateLimit-Remaining": str(remaining),
                "X-RateLimit-Reset":     str(reset_in),
                "Retry-After":           str(int(reset_in) + 1),
            },
        )

    # Attach rate limit info to request.state so middleware can add headers to response
    request.state.rate_limit_remaining = remaining
    request.state.rate_limit_reset = reset_in
    request.state.rate_limit_limit = limit
    return current_user


def get_rate_limiter() -> RateLimiter:
    """Return the module-level singleton RateLimiter (for testing/introspection)."""
    return _rate_limiter
