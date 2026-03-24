"""AntiGravity Scheduler API Gateway package.

OS Analogy: the syscall interface provided by the kernel is a thin wrapper
around internal kernel subsystems.  This package exposes authentication,
rate-limiting and HTTP routes that wrap the scheduler core (phase 2).

The file is intentionally empty aside from this docstring; it simply makes
`gateway/` a Python package so that submodules can be imported using
`from gateway import auth, rate_limit, main` regardless of the current
working directory.
"""
