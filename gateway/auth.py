# -*- coding: utf-8 -*-
"""
auth.py -- AntiGravity Scheduler: JWT Authentication & Role-Based Access Control
==================================================================================

OS Analogy: Kernel Security & Capability System
-------------------------------------------------
Authentication in a web API mirrors the OS concept of user identity and privilege rings:

  - Password verification  = PAM (Pluggable Authentication Modules) in Linux
  - JWT token              = Kerberos ticket / access token -- a cryptographically signed
                             proof of identity that services can verify without calling back
                             to a central authority on every request.
  - Role-based access      = Linux DAC (Discretionary Access Control) + capability sets.
                             Just as `CAP_NET_ADMIN` lets a process configure network
                             interfaces while normal processes cannot, the "admin" role
                             allows task deletion while "readonly" cannot.

JWT Structure (header.payload.signature):
-----------------------------------------
A JWT is three base64url-encoded JSON objects separated by dots:

  HEADER  : {"alg": "HS256", "typ": "JWT"}
  PAYLOAD : {"sub": "alice", "role": "user", "exp": 1234567890}
  SIGNATURE: HMAC-SHA256(base64(header) + "." + base64(payload), SECRET_KEY)

Why Bearer tokens?
  The Bearer scheme (RFC 6750) lets the client prove identity by simply
  *possessing* the token -- like a signed check. No session cookies, no
  server-side session store. The server is stateless, just like a kernel
  verifying a capability bit in a file descriptor, not a registry.

Roles:
  admin    -- full control (submit, cancel, see metrics)
  user     -- submit tasks, mark complete
  readonly -- GET-only: list tasks, view cluster
"""

from __future__ import annotations

import os
from datetime import datetime, timedelta, timezone
from typing import Optional

from fastapi import Depends, HTTPException, status
from fastapi.security import OAuth2PasswordBearer
from jose import JWTError, jwt
from passlib.context import CryptContext
from pydantic import BaseModel

# ---------------------------------------------------------------------------
# Constants -- load from environment with sane defaults for local dev
# ---------------------------------------------------------------------------

# In production this MUST come from a secret manager (AWS Secrets Manager,
# HashiCorp Vault, K8s Secret). The fallback is only for local development.
SECRET_KEY: str = os.getenv(
    "ANTIGRAVITY_SECRET_KEY",
    "super-secret-dev-key-change-in-production-32-chars!!"
)
ALGORITHM = "HS256"                        # HMAC-SHA256 -- fast symmetric signing
ACCESS_TOKEN_EXPIRE_MINUTES: int = 30      # Short-lived tokens -- like OS session cookies

# ---------------------------------------------------------------------------
# Crypto context -- bcrypt with auto-selected work factor
# ---------------------------------------------------------------------------
# bcrypt is the standard for password hashing because it's:
#   1. Intentionally slow (work factor = 2^12 iterations by default)
#   2. Self-salting -- every call produces a different hash
#   3. Resistant to rainbow tables and GPU cracking
pwd_context = CryptContext(schemes=["bcrypt"], deprecated="auto")

# OAuth2 token URL -- FastAPI's OpenAPI UI uses this for the "Authorize" button
oauth2_scheme = OAuth2PasswordBearer(tokenUrl="/auth/token")


# ---------------------------------------------------------------------------
# Pydantic models (request/response schemas)
# ---------------------------------------------------------------------------

class Token(BaseModel):
    """HTTP response body for a successful /auth/token login."""
    access_token: str
    token_type: str = "bearer"


class TokenData(BaseModel):
    """Decoded payload extracted from a validated JWT."""
    username: Optional[str] = None
    role: str = "readonly"


class UserInDB(BaseModel):
    """
    In-memory user record -- analogous to /etc/shadow in Linux.
    In a real system this would come from a DB row (PostgreSQL, LDAP, etc.).
    """
    username: str
    hashed_password: str
    role: str                 # "admin" | "user" | "readonly"
    is_active: bool = True


# ---------------------------------------------------------------------------
# Fake in-memory user database
# Passwords are pre-hashed with bcrypt.
#
# Plaintext passwords (for testing):
#   admin  -> "adminpass"
#   alice  -> "alicepass"
#   bob    -> "bobpass"
# ---------------------------------------------------------------------------

def _hash(pw: str) -> str:
    """Convenience wrapper to pre-hash test passwords at module load time."""
    return pwd_context.hash(pw)


FAKE_USERS_DB: dict[str, UserInDB] = {
    "admin": UserInDB(
        username="admin",
        hashed_password=_hash("adminpass"),
        role="admin",
        is_active=True,
    ),
    "alice": UserInDB(
        username="alice",
        hashed_password=_hash("alicepass"),
        role="user",
        is_active=True,
    ),
    "bob": UserInDB(
        username="bob",
        hashed_password=_hash("bobpass"),
        role="readonly",
        is_active=True,
    ),
}


# ---------------------------------------------------------------------------
# Core auth functions
# ---------------------------------------------------------------------------

def verify_password(plain_password: str, hashed_password: str) -> bool:
    """
    Verify a plaintext password against its bcrypt hash.

    OS Analogy: Linux's crypt(3) function used by PAM modules to validate
    passwords from /etc/shadow. The comparison is constant-time to prevent
    timing oracles (an attacker measuring response time to infer hash bits).

    passlib's verify() is already timing-safe.
    """
    return pwd_context.verify(plain_password, hashed_password)


def get_user(username: str) -> Optional[UserInDB]:
    """
    Look up a user record by username.
    Analogous to getpwnam(3) that looks up /etc/passwd.
    """
    return FAKE_USERS_DB.get(username)


def authenticate_user(username: str, password: str) -> Optional[UserInDB]:
    """
    Authenticate: look up user + verify password.

    OS Analogy: PAM's pam_authenticate() -- validates credentials and returns
    the authenticated identity or None. Two-step: (1) user must exist,
    (2) password must match stored hash. Deliberate constant-time design.
    """
    user = get_user(username)
    if not user:
        # Still verify a dummy hash to prevent username enumeration via timing.
        pwd_context.verify(password, "$2b$12$dummyhashtopreventtimingattacks000")
        print(f"[AUTH] Login failed: unknown user '{username}'")
        return None
    if not verify_password(password, user.hashed_password):
        print(f"[AUTH] Login failed: wrong password for '{username}'")
        return None
    if not user.is_active:
        print(f"[AUTH] Login rejected: user '{username}' is inactive")
        return None
    print(f"[AUTH] Login success: '{username}' (role={user.role})")
    return user


def create_access_token(data: dict, expires_delta: Optional[timedelta] = None) -> str:
    """
    Sign and encode a JWT access token.

    The JWT payload (claims) follows RFC 7519 standard claims:
      sub  = subject (username)
      exp  = expiration Unix timestamp
      role = custom claim for RBAC

    OS Analogy: Issuing a Kerberos service ticket -- the KDC signs a token that
    the client presents to services to prove identity without re-authenticating.
    The signature ensures the token cannot be forged without the SECRET_KEY.

    The exp claim enforces token lifetime -- expired tokens are rejected exactly
    like expired sudo sessions. Short expiry + refresh tokens is the production
    pattern (analogous to short-lived session cookies with remember-me tokens).
    """
    to_encode = data.copy()
    expire = datetime.now(timezone.utc) + (
        expires_delta if expires_delta else timedelta(minutes=ACCESS_TOKEN_EXPIRE_MINUTES)
    )
    to_encode.update({"exp": expire})
    encoded_jwt = jwt.encode(to_encode, SECRET_KEY, algorithm=ALGORITHM)
    print(f"[AUTH] Issued JWT for '{data.get('sub')}' | expires in "
          f"{(expires_delta or timedelta(minutes=ACCESS_TOKEN_EXPIRE_MINUTES)).seconds // 60}m")
    return encoded_jwt


async def get_current_user(token: str = Depends(oauth2_scheme)) -> UserInDB:
    """
    FastAPI dependency: decode + validate the JWT and return the authenticated user.

    This is injected into every protected route via `Depends(get_current_user)`.
    FastAPI calls this automatically before the route handler runs -- analogous
    to a kernel security hook (LSM hook in Linux) that gates every syscall.

    Raises HTTP 401 if:
      - Token is missing (OAuth2PasswordBearer raises this automatically)
      - Token signature is invalid (tampered)
      - Token is expired (exp claim in the past)
      - Decoded username does not exist in the user DB
    """
    credentials_exception = HTTPException(
        status_code=status.HTTP_401_UNAUTHORIZED,
        detail="Could not validate credentials",
        headers={"WWW-Authenticate": "Bearer"},
    )
    try:
        # jwt.decode() verifies signature AND expiry atomically.
        payload = jwt.decode(token, SECRET_KEY, algorithms=[ALGORITHM])
        username: Optional[str] = payload.get("sub")
        role: str = payload.get("role", "readonly")
        if username is None:
            raise credentials_exception
        token_data = TokenData(username=username, role=role)
    except JWTError as exc:
        print(f"[AUTH] JWT validation failed: {exc}")
        raise credentials_exception

    user = get_user(token_data.username)
    if user is None:
        print(f"[AUTH] Token references unknown user '{token_data.username}'")
        raise credentials_exception
    if not user.is_active:
        raise HTTPException(status_code=status.HTTP_400_BAD_REQUEST,
                            detail="Inactive user")
    return user


def require_role(*roles: str):
    """
    Factory that returns a FastAPI dependency enforcing role-based access control.

    Usage in a route:
        @app.delete("/tasks/{id}", dependencies=[Depends(require_role("admin"))])

    OS Analogy: Linux capabilities (CAP_SYS_ADMIN, CAP_NET_BIND_SERVICE, etc.).
    Just as a process must hold a specific capability to perform privileged
    syscalls, a user must hold a specific role to call privileged API routes.

    Raises HTTP 403 Forbidden if the user's role is not in the allowed set.
    403 vs 401: 401 = "who are you?", 403 = "I know who you are, but NO."
    """
    async def role_checker(
        current_user: UserInDB = Depends(get_current_user)
    ) -> UserInDB:
        if current_user.role not in roles:
            print(f"[AUTH] Access denied: '{current_user.username}' "
                  f"(role={current_user.role}) tried to access {roles}-only endpoint")
            raise HTTPException(
                status_code=status.HTTP_403_FORBIDDEN,
                detail=f"Insufficient permissions. Required role: {list(roles)}, "
                       f"your role: {current_user.role}",
            )
        return current_user
    return role_checker
