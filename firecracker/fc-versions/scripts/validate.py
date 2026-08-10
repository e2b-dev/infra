#!/usr/bin/env python3
"""
Validation script for Firecracker release workflow.

This script validates inputs, resolves tags/commits, checks CI status,
and determines which architectures need to be built.
"""

import argparse
import json
import os
import subprocess
import sys
from dataclasses import dataclass
from typing import Optional


FIRECRACKER_REPO = "e2b-dev/firecracker"


def run_command(cmd: list[str], check: bool = True) -> subprocess.CompletedProcess:
    """Run a command and return the result."""
    return subprocess.run(cmd, capture_output=True, text=True, check=check)


def gh_api(endpoint: str) -> Optional[dict]:
    """Call the GitHub API using the gh CLI."""
    # Route firecracker-repo calls through the App token when present, so
    # they don't inherit GH_TOKEN, which is scoped to the current repo.
    env = os.environ.copy()
    firecracker_token = env.get("FIRECRACKER_GH_TOKEN")
    if firecracker_token:
        env["GH_TOKEN"] = firecracker_token
    result = subprocess.run(
        ["gh", "api", endpoint],
        capture_output=True,
        text=True,
        check=False,
        env=env,
    )
    if result.returncode != 0:
        return None
    return json.loads(result.stdout)


def validate_inputs(tag: Optional[str], commit_hash: Optional[str], build_amd64: bool, build_arm64: bool) -> Optional[str]:
    """Validate inputs."""
    if not build_amd64 and not build_arm64:
        return "At least one architecture must be selected"
    if not tag and not commit_hash:
        return "Either tag or commit_hash must be provided"
    return None


def resolve_tag_to_commit(tag: str, repo: str = FIRECRACKER_REPO) -> tuple[str, Optional[str]]:
    """
    Resolve a tag to its commit hash.

    Returns (commit_hash, error_message).
    """
    data = gh_api(f"repos/{repo}/git/ref/tags/{tag}")
    if not data:
        return "", f"Tag {tag} does not exist in {repo} repository"

    commit_hash = data["object"]["sha"]

    # Handle annotated tags (need to dereference to get commit SHA)
    tag_object = gh_api(f"repos/{repo}/git/tags/{commit_hash}")
    if tag_object and "object" in tag_object:
        commit_hash = tag_object["object"]["sha"]

    return commit_hash, None


def validate_commit(commit_hash: str, repo: str = FIRECRACKER_REPO) -> tuple[str, Optional[str]]:
    """
    Validate that a commit exists.

    Returns (full_sha, error_message).
    """
    data = gh_api(f"repos/{repo}/commits/{commit_hash}")
    if not data:
        return "", f"Commit {commit_hash} does not exist in {repo} repository"
    return data["sha"], None


def find_tag_for_commit(commit_hash: str, repo: str = FIRECRACKER_REPO) -> tuple[str, Optional[str]]:
    """
    Find the most recent tag that is an ancestor of (or equal to) the given commit.

    Returns (tag_name, error_message).
    """
    # List tags (GitHub returns them in reverse chronological order by default)
    tags_data = gh_api(f"repos/{repo}/tags?per_page=100")
    if not tags_data:
        return "", "Failed to fetch tags from repository"

    for tag_info in tags_data:
        tag_name = tag_info["name"]
        tag_commit = tag_info["commit"]["sha"]

        # Check if this tag's commit is the same as our target
        if tag_commit == commit_hash:
            return tag_name, None

        # Check if tag is an ancestor of our commit using compare API
        compare_data = gh_api(f"repos/{repo}/compare/{tag_commit}...{commit_hash}")
        if compare_data and compare_data.get("status") in ("ahead", "identical"):
            return tag_name, None

    return "", f"No tag found that is an ancestor of commit {commit_hash}"


def resolve_tag_and_commit(
    tag: Optional[str],
    input_hash: Optional[str],
    repo: str = FIRECRACKER_REPO
) -> tuple[str, str, Optional[str]]:
    """
    Resolve tag and commit hash.

    Returns (tag, commit_hash, error_message).
    """
    if tag and input_hash:
        # Both provided: validate commit exists and is at or after the tag
        commit_hash, error = validate_commit(input_hash, repo)
        if error:
            return "", "", error

        # Resolve tag to its commit
        tag_commit, error = resolve_tag_to_commit(tag, repo)
        if error:
            return "", "", error

        # Verify commit is at or after the tag (in the same tree)
        if commit_hash != tag_commit:
            compare_data = gh_api(f"repos/{repo}/compare/{tag_commit}...{commit_hash}")
            if not compare_data:
                return "", "", f"Failed to compare tag {tag} with commit {input_hash}"

            status = compare_data.get("status")
            if status not in ("ahead", "identical"):
                return "", "", (
                    f"Commit {input_hash[:7]} is not at or after tag {tag}. "
                    f"The commit must be in the same tree and after the tag. "
                    f"(compare status: {status})"
                )

        return tag, commit_hash, None

    if tag:
        # Only tag provided: resolve to commit
        commit_hash, error = resolve_tag_to_commit(tag, repo)
        if error:
            return "", "", error
        return tag, commit_hash, None

    if input_hash:
        # Only commit provided: validate and find tag
        commit_hash, error = validate_commit(input_hash, repo)
        if error:
            return "", "", error

        resolved_tag, error = find_tag_for_commit(commit_hash, repo)
        if error:
            return "", "", error
        return resolved_tag, commit_hash, None

    return "", "", "Either tag or commit_hash must be provided"


# IGNORED_STATUS_CONTEXTS lists legacy commit-status contexts that should not
# block a release build even when failing. Keep the set tiny and well-justified.
#
# verification/cla-signed: cla-bot fails on the upstream firecracker fork
# whenever a backport branch carries commits authored by upstream maintainers
# we don't have a CLA for (e.g. ilstam, ShadowCurse, JackThomson2). Those
# contributors won't ever sign our CLA, so the status is permanently red on
# every direct-mem / hint backport branch — we still want to ship those builds.
IGNORED_STATUS_CONTEXTS = frozenset({"verification/cla-signed"})

# IGNORED_CHECK_NAMES is the equivalent for the Checks API (apps that file a
# check-run rather than a legacy status). Empty today; mirror IGNORED_STATUS_CONTEXTS
# if a check-run-based bot ever ends up in the same situation.
IGNORED_CHECK_NAMES = frozenset()


def _rollup_status(statuses: list[dict]) -> tuple[str, int]:
    """Compute (state, count) over the statuses list, mirroring how GitHub's
    combined-status endpoint rolls up: any failure → failure, else any pending
    → pending, else any success → success, else unknown.
    """
    if not statuses:
        return "unknown", 0
    states = {s.get("state") for s in statuses}
    if "failure" in states or "error" in states:
        return "failure", len(statuses)
    if "pending" in states:
        return "pending", len(statuses)
    if "success" in states:
        return "success", len(statuses)
    return "unknown", len(statuses)


def check_ci_status(commit_hash: str, repo: str = FIRECRACKER_REPO) -> tuple[bool, str]:
    """
    Check CI status for a commit.

    Returns (success, message).
    """
    # Check commit status API. Filter out IGNORED_STATUS_CONTEXTS and recompute
    # the rollup so a single permanently-red status (e.g. cla-bot on
    # external-contributor backport branches) doesn't block release builds.
    status_response = gh_api(f"/repos/{repo}/commits/{commit_hash}/status")
    if not status_response:
        status_response = {"state": "unknown", "total_count": 0, "statuses": []}

    raw_statuses = status_response.get("statuses", []) or []
    ignored_status_contexts = [
        s.get("context") for s in raw_statuses
        if s.get("context") in IGNORED_STATUS_CONTEXTS
    ]
    filtered_statuses = [
        s for s in raw_statuses
        if s.get("context") not in IGNORED_STATUS_CONTEXTS
    ]
    if ignored_status_contexts:
        status, status_count = _rollup_status(filtered_statuses)
        print(
            f"Status API: ignoring contexts {sorted(set(ignored_status_contexts))} "
            f"→ rollup state={status}, count={status_count}",
            file=sys.stderr,
        )
    else:
        status = status_response.get("state", "unknown")
        status_count = status_response.get("total_count", 0)
        print(f"Status API: state={status}, count={status_count}", file=sys.stderr)

    # Check check-runs API. Same filter for IGNORED_CHECK_NAMES.
    check_response = gh_api(f"/repos/{repo}/commits/{commit_hash}/check-runs")
    if not check_response:
        check_response = {"total_count": 0, "check_runs": []}

    raw_check_runs = check_response.get("check_runs", []) or []
    ignored_check_names = [
        cr.get("name") for cr in raw_check_runs
        if cr.get("name") in IGNORED_CHECK_NAMES
    ]
    check_runs = [
        cr for cr in raw_check_runs
        if cr.get("name") not in IGNORED_CHECK_NAMES
    ]
    check_count = len(check_runs)

    # Determine check conclusion
    if check_count == 0:
        check_conclusion = "no_checks"
    elif any(cr.get("status") in ("in_progress", "queued") for cr in check_runs):
        check_conclusion = "pending"
    elif any(cr.get("conclusion") in ("failure", "cancelled", "timed_out") for cr in check_runs):
        check_conclusion = "failure"
    elif all(cr.get("conclusion") in ("success", "skipped", "neutral") for cr in check_runs):
        check_conclusion = "success"
    else:
        check_conclusion = "unknown"

    if ignored_check_names:
        print(
            f"Check-runs API: ignoring {sorted(set(ignored_check_names))} "
            f"→ conclusion={check_conclusion}, count={check_count}",
            file=sys.stderr,
        )
    else:
        print(f"Check-runs API: conclusion={check_conclusion}, count={check_count}", file=sys.stderr)

    if status == "failure" or check_conclusion == "failure":
        return False, f"CI failed for commit {commit_hash} - refusing to build"

    if check_conclusion == "pending" or (status == "pending" and status_count > 0):
        return False, f"CI is still running for commit {commit_hash} - refusing to build"

    if status == "success" or check_conclusion == "success":
        return True, f"CI passed for commit {commit_hash}"

    if status_count == 0 and check_count == 0:
        print(f"::warning::No CI checks found for commit {commit_hash} - proceeding anyway", file=sys.stderr)
        return True, f"No CI checks found for commit {commit_hash} - proceeding anyway"

    print(f"::warning::Could not definitively verify CI status - proceeding anyway", file=sys.stderr)
    return True, f"Could not definitively verify CI status (status={status}, check_conclusion={check_conclusion}) - proceeding anyway"


def get_existing_release_assets(version_name: str) -> set[str]:
    """
    Get the set of existing asset names for a release.

    Returns empty set if release doesn't exist.
    """
    repo = os.environ.get("GITHUB_REPOSITORY", "")
    if not repo:
        return set()

    result = run_command(
        ["gh", "release", "view", version_name, "--json", "assets", "-q", ".assets[].name"],
        check=False
    )
    if result.returncode != 0:
        return set()

    return set(result.stdout.strip().split("\n")) if result.stdout.strip() else set()


def check_artifacts_needed(version_name: str, build_amd64: bool, build_arm64: bool) -> bool:
    """
    Check if any requested architectures are missing an artifact from the release.

    Returns True if at least one artifact needs to be built and uploaded. Mirrors
    the build job's skip-check: a release needs both the prod binary
    (firecracker-<arch>) and the gdb-enabled debug binary (firecracker-debug-<arch>),
    so a release that has the prod binary but not the debug one still has new
    artifacts to publish.
    """
    existing_assets = get_existing_release_assets(version_name)

    archs = []
    if build_amd64:
        archs.append("amd64")
    if build_arm64:
        archs.append("arm64")

    for arch in archs:
        if f"firecracker-{arch}" not in existing_assets:
            return True
        if f"firecracker-debug-{arch}" not in existing_assets:
            return True

    return False


def generate_build_matrix(build_amd64: bool, build_arm64: bool) -> dict:
    """
    Generate build matrix for all requested architectures.

    Build and deploy jobs always run; individual steps check for existing artifacts.
    """
    include = []
    if build_amd64:
        include.append({"arch": "amd64", "runner": "ubuntu-24.04"})
    if build_arm64:
        include.append({"arch": "arm64", "runner": "ubuntu-24.04-arm"})

    return {"include": include}


def write_github_output(outputs: dict[str, str]) -> None:
    """Write outputs to GITHUB_OUTPUT file."""
    output_file = os.environ.get("GITHUB_OUTPUT")
    if output_file:
        with open(output_file, "a") as f:
            for key, value in outputs.items():
                f.write(f"{key}={value}\n")
    else:
        # For local testing, print to stdout
        for key, value in outputs.items():
            print(f"{key}={value}")


def main() -> int:
    parser = argparse.ArgumentParser(description="Validate Firecracker release inputs")
    parser.add_argument("--tag", default="", help="Firecracker version tag (e.g., v1.14.1)")
    parser.add_argument("--commit-hash", default="", help="Full commit hash to build")
    parser.add_argument("--build-amd64", type=lambda x: x.lower() == "true", default=True,
                        help="Build for amd64 architecture")
    parser.add_argument("--build-arm64", type=lambda x: x.lower() == "true", default=True,
                        help="Build for arm64 architecture")

    args = parser.parse_args()

    tag = args.tag if args.tag else None
    commit_hash_input = args.commit_hash if args.commit_hash else None

    # Step 1: Validate inputs
    error = validate_inputs(tag, commit_hash_input, args.build_amd64, args.build_arm64)
    if error:
        print(f"::error::{error}", file=sys.stderr)
        return 1

    # Step 2: Resolve tag and commit hash
    if tag:
        print(f"Resolving tag {tag}...", file=sys.stderr)
    else:
        print(f"Finding tag for commit {commit_hash_input}...", file=sys.stderr)

    tag, commit_hash, error = resolve_tag_and_commit(tag, commit_hash_input)
    if error:
        print(f"::error::{error}", file=sys.stderr)
        return 1

    short_hash = commit_hash[:7]
    version_name = f"{tag}_{short_hash}"

    print(f"Tag: {tag}", file=sys.stderr)
    print(f"Full commit hash: {commit_hash}", file=sys.stderr)
    print(f"Short hash: {short_hash}", file=sys.stderr)
    print(f"Version name: {version_name}", file=sys.stderr)

    # Step 3: Check CI status
    print(f"Checking CI status for commit {commit_hash}...", file=sys.stderr)
    ci_ok, ci_message = check_ci_status(commit_hash)
    if not ci_ok:
        print(f"::error::{ci_message}", file=sys.stderr)
        return 1
    print(ci_message, file=sys.stderr)

    # Step 4: Generate build matrix for all requested architectures
    build_matrix = generate_build_matrix(args.build_amd64, args.build_arm64)

    print(f"Build matrix: {json.dumps(build_matrix)}", file=sys.stderr)

    # Step 5: Check if any artifacts need to be built
    has_new_artifacts = check_artifacts_needed(version_name, args.build_amd64, args.build_arm64)
    print(f"Has new artifacts to build: {has_new_artifacts}", file=sys.stderr)

    # Write outputs
    write_github_output({
        "commit_hash": commit_hash,
        "version_name": version_name,
        "build_matrix": json.dumps(build_matrix),
        "has_new_artifacts": str(has_new_artifacts).lower(),
    })

    return 0


if __name__ == "__main__":
    sys.exit(main())
