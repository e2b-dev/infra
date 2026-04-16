#!/usr/bin/env python3
"""
Integration test for the FS-only pause/resume envd endpoints.

Tests the guest-agent HTTP endpoints on a running E2B sandbox:
  POST /fs-snapshot/sync
  POST /fs-snapshot/mount-overlay
  POST /fs-snapshot/unmount-overlay

Usage:
  pip install e2b requests
  E2B_API_KEY=... python test_envd_endpoints.py

Requires the envd binary with fssnapshot support deployed to the cluster.
"""

import os
import sys
import time
import requests
from e2b import Sandbox


ENVD_PORT = 49983


def get_envd_url(sbx: Sandbox, path: str) -> str:
    """Build the direct HTTP URL to envd inside the sandbox."""
    return f"http://{sbx.get_host(ENVD_PORT)}{path}"


def test_health(sbx: Sandbox):
    """Verify basic envd health endpoint works."""
    url = get_envd_url(sbx, "/health")
    r = requests.get(url, timeout=5)
    assert r.status_code == 204, f"health check failed: {r.status_code}"
    print("  PASS: /health -> 204")


def test_sync(sbx: Sandbox):
    """Test the filesystem sync endpoint."""
    # Write a file first so there's something to sync
    sbx.files.write("/tmp/test-sync.txt", "hello from sync test")

    url = get_envd_url(sbx, "/fs-snapshot/sync")
    r = requests.post(url, timeout=10)
    assert r.status_code == 200, f"sync failed: {r.status_code} {r.text}"
    print("  PASS: /fs-snapshot/sync -> 200")


def test_mount_overlay_fails_without_vdb(sbx: Sandbox):
    """
    Test that mount-overlay returns an error when /dev/vdb doesn't exist.
    This is expected in the current single-disk setup — the endpoint itself
    works, it just can't find the overlay device.
    """
    url = get_envd_url(sbx, "/fs-snapshot/mount-overlay")
    r = requests.post(url, timeout=10)
    # Should fail because /dev/vdb doesn't exist in the current setup
    assert r.status_code == 500, f"expected 500 (no /dev/vdb), got {r.status_code}"
    assert "mount" in r.text.lower() or "vdb" in r.text.lower(), f"unexpected error: {r.text}"
    print(f"  PASS: /fs-snapshot/mount-overlay -> 500 (expected, no /dev/vdb): {r.text.strip()}")


def test_unmount_overlay_fails_when_not_mounted(sbx: Sandbox):
    """
    Test that unmount-overlay returns an error when no overlay is mounted.
    This validates the endpoint exists and runs the teardown logic.
    """
    url = get_envd_url(sbx, "/fs-snapshot/unmount-overlay")
    r = requests.post(url, timeout=10)
    # Should fail because there's no overlay to unmount
    assert r.status_code == 500, f"expected 500 (no overlay), got {r.status_code}"
    print(f"  PASS: /fs-snapshot/unmount-overlay -> 500 (expected, no overlay): {r.text.strip()}")


def test_file_persistence(sbx: Sandbox):
    """
    Verify basic file operations still work — this is the baseline that
    FS-only pause/resume needs to preserve.
    """
    test_content = f"test-{time.time()}"
    sbx.files.write("/home/user/test-persist.txt", test_content)
    result = sbx.files.read("/home/user/test-persist.txt")
    assert result == test_content, f"file content mismatch: {result!r} != {test_content!r}"
    print("  PASS: file write/read roundtrip")


def main():
    api_key = os.environ.get("E2B_API_KEY")
    if not api_key:
        print("ERROR: Set E2B_API_KEY environment variable")
        sys.exit(1)

    print("Creating sandbox...")
    sbx = Sandbox()
    print(f"Sandbox ID: {sbx.sandbox_id}")

    try:
        print("\n--- Testing envd endpoints ---")
        test_health(sbx)
        test_sync(sbx)
        test_file_persistence(sbx)
        test_mount_overlay_fails_without_vdb(sbx)
        test_unmount_overlay_fails_when_not_mounted(sbx)

        print("\n--- All tests passed ---")
        print(
            "\nNote: mount-overlay and unmount-overlay correctly fail because\n"
            "the current setup uses a single disk. The two-disk OverlayFS\n"
            "setup requires changes to the rootfs image and FC drive config."
        )
    finally:
        print(f"\nKilling sandbox {sbx.sandbox_id}...")
        sbx.kill()


if __name__ == "__main__":
    main()
