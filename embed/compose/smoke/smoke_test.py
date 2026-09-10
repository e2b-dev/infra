#!/usr/bin/env python3
"""Host-side smoke test, run on the KVM host. Requires: pip install e2b==2.46.0 and the E2B_API_URL,
E2B_API_KEY, E2B_SANDBOX_URL variables shown by docker compose logs ready."""
import os
import sys

from e2b import Sandbox

for name in ("E2B_API_URL", "E2B_API_KEY", "E2B_SANDBOX_URL"):
    if not os.environ.get(name):
        print(f"smoke: {name} is required", file=sys.stderr)
        print(
            "FIX: export the E2B_API_URL, E2B_SANDBOX_URL and E2B_API_KEY values shown by docker compose logs ready",
            file=sys.stderr,
        )
        sys.exit(2)

sbx = None
failed = False
try:
    sbx = Sandbox.create("base", timeout=120)
    result = sbx.commands.run("echo hello")
    if result.exit_code != 0 or result.stdout.strip() != "hello":
        raise RuntimeError(f"unexpected command result: {result!r}")
    print(f"smoke: ok sandbox={sbx.sandbox_id} host={sbx.get_host(8080)}")
except Exception as err:
    print(f"smoke: failed: {err}", file=sys.stderr)
    failed = True
finally:
    # Ending the sandbox is part of what this test checks, so a kill failure is
    # still a failure: the exit status stays non-zero. It must read as a FIX
    # line though, never as a traceback out of the finally block. The FIX lines
    # are printed here, after the kill, so that when the test itself failed its
    # own FIX line is the last thing on stderr.
    # stdout is block-buffered when it is a pipe rather than a terminal, so the
    # "smoke: ok" line above would otherwise flush at exit — after these stderr
    # lines — and the FIX line would not be last in a combined log.
    sys.stdout.flush()
    kill_failed = False
    if sbx is not None:
        try:
            sbx.kill()
        except Exception as err:
            print(f"smoke: the sandbox could not be killed: {err}", file=sys.stderr)
            kill_failed = True
    if failed:
        print("FIX: run docker compose logs api orchestrator client-proxy", file=sys.stderr)
        sys.exit(1)
    if kill_failed:
        print(
            "FIX: the sandbox could not be killed; run docker compose logs api orchestrator",
            file=sys.stderr,
        )
        sys.exit(1)
