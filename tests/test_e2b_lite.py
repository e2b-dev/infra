#!/usr/bin/env python3
"""
E2B Lite Test Script

Tests basic sandbox functionality: creation, commands, filesystem.

Usage:
    pip install e2b
    python tests/test_e2b_lite.py
"""

import os
import subprocess
import sys

try:
    from e2b import Sandbox
except ImportError:
    print("Error: e2b package not installed")
    print("Install with: pip install e2b")
    sys.exit(1)


def get_template_id_from_db():
    """Query PostgreSQL for the template ID."""
    try:
        result = subprocess.run(
            ["docker", "exec", "local-dev-postgres-1", "psql", "-U", "postgres", "-tAc", "SELECT id FROM envs LIMIT 1;"],
            capture_output=True,
            text=True,
            timeout=10,
        )
        if result.returncode == 0 and result.stdout.strip():
            return result.stdout.strip()
    except Exception:
        pass
    return None


# Configuration
API_KEY = os.environ.get("E2B_API_KEY", "e2b_53ae1fed82754c17ad8077fbc8bcdd90")
API_URL = os.environ.get("E2B_API_URL", "http://localhost:80")
SANDBOX_URL = os.environ.get("E2B_SANDBOX_URL", "http://localhost:3002")

# Get template ID: from env var, from database, or fail
TEMPLATE_ID = os.environ.get("E2B_TEMPLATE_ID")
if not TEMPLATE_ID:
    TEMPLATE_ID = get_template_id_from_db()

if not TEMPLATE_ID:
    print("=" * 50)
    print("  E2B Lite Test - ERROR")
    print("=" * 50)
    print()
    print("No template found!")
    print()
    print("Either:")
    print("  1. Set E2B_TEMPLATE_ID environment variable")
    print("  2. Build a template: ./scripts/e2b-lite-setup.sh")
    print()
    print("To check database:")
    print("  docker exec local-dev-postgres-1 psql -U postgres -c 'SELECT id FROM envs;'")
    sys.exit(1)

print("=" * 50)
print("  E2B Lite Test")
print("=" * 50)
print()
print(f"API URL:     {API_URL}")
print(f"Sandbox URL: {SANDBOX_URL}")
print(f"Template:    {TEMPLATE_ID}")
print()

try:
    print("1. Creating sandbox...")
    sandbox = Sandbox.create(
        template=TEMPLATE_ID,
        api_url=API_URL,
        sandbox_url=SANDBOX_URL,
        timeout=120,
        api_key=API_KEY,
    )
    print(f"   ✓ Sandbox ID: {sandbox.sandbox_id}")
    print()

    print("2. Running command...")
    result = sandbox.commands.run("echo 'Hello from E2B Lite!' && uname -a", user="root")
    print(f"   ✓ Output: {result.stdout.strip()}")
    print()

    print("3. Writing file via command...")
    sandbox.commands.run("echo 'Hello World from E2B!' > /tmp/test.txt", user="root")
    print("   ✓ Written /tmp/test.txt")
    print()

    print("4. Reading file via command...")
    result = sandbox.commands.run("cat /tmp/test.txt", user="root")
    print(f"   ✓ Content: {result.stdout.strip()}")
    print()

    print("5. Listing directory via command...")
    result = sandbox.commands.run("ls /tmp | head -5", user="root")
    print(f"   ✓ Files: {result.stdout.strip()}")
    print()

    print("6. Running Python...")
    result = sandbox.commands.run("python3 -c \"print(2+2)\"", user="root")
    print(f"   ✓ 2+2 = {result.stdout.strip()}")
    print()

    sandbox.kill()
    print("=" * 50)
    print("  All tests passed!")
    print("=" * 50)

except Exception as e:
    print(f"\n❌ Error: {e}")
    print("\nTroubleshooting:")
    print("  1. Ensure all services are running:")
    print("     ./scripts/services/start-all.sh")
    print("  2. Check service logs:")
    print("     tail -f /tmp/e2b-*.log")
    print("  3. Verify template exists in database:")
    print("     docker exec local-dev-postgres-1 psql -U postgres -c 'SELECT id FROM envs;'")
    sys.exit(1)
