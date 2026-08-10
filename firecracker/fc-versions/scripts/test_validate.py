#!/usr/bin/env python3
"""Tests for validate.py"""

import json
import subprocess
from unittest.mock import patch, MagicMock

import pytest

from validate import (
    validate_inputs,
    resolve_tag_to_commit,
    validate_commit,
    find_tag_for_commit,
    resolve_tag_and_commit,
    check_ci_status,
    generate_build_matrix,
    get_existing_release_assets,
    check_artifacts_needed,
    gh_api,
)


# Test fixtures
SAMPLE_COMMIT_SHA = "abc123def456789012345678901234567890abcd"
SAMPLE_TAG_COMMIT_SHA = "def456abc789012345678901234567890abcdef12"


def mock_run_command(responses: dict):
    """Create a mock for run_command that returns predefined responses."""
    def _mock(cmd: list[str], check: bool = True):
        key = " ".join(cmd)
        for pattern, response in responses.items():
            if pattern in key:
                result = MagicMock()
                result.returncode = response.get("returncode", 0)
                result.stdout = response.get("stdout", "")
                result.stderr = response.get("stderr", "")
                return result
        # Default: command not found
        result = MagicMock()
        result.returncode = 1
        result.stdout = ""
        result.stderr = "not found"
        return result
    return _mock


class TestValidateInputs:
    """Tests for validate_inputs function."""

    def test_no_architectures_selected(self):
        error = validate_inputs("v1.0.0", None, False, False)
        assert error == "At least one architecture must be selected"

    def test_no_tag_or_commit(self):
        error = validate_inputs(None, None, True, True)
        assert error == "Either tag or commit_hash must be provided"

    def test_tag_only_valid(self):
        error = validate_inputs("v1.0.0", None, True, False)
        assert error is None

    def test_commit_only_valid(self):
        error = validate_inputs(None, SAMPLE_COMMIT_SHA, False, True)
        assert error is None

    def test_both_tag_and_commit_valid(self):
        error = validate_inputs("v1.0.0", SAMPLE_COMMIT_SHA, True, True)
        assert error is None

    def test_amd64_only(self):
        error = validate_inputs("v1.0.0", None, True, False)
        assert error is None

    def test_arm64_only(self):
        error = validate_inputs("v1.0.0", None, False, True)
        assert error is None


class TestResolveTagToCommit:
    """Tests for resolve_tag_to_commit function."""

    def test_tag_not_found(self):
        with patch("validate.gh_api", return_value=None):
            commit, error = resolve_tag_to_commit("v1.0.0")
            assert commit == ""
            assert "does not exist" in error

    def test_lightweight_tag(self):
        """Test resolving a lightweight tag (points directly to commit)."""
        with patch("validate.gh_api") as mock_api:
            # First call: get tag ref
            mock_api.side_effect = [
                {"object": {"sha": SAMPLE_COMMIT_SHA}},
                None,  # No tag object (lightweight tag)
            ]
            commit, error = resolve_tag_to_commit("v1.0.0")
            assert commit == SAMPLE_COMMIT_SHA
            assert error is None

    def test_annotated_tag(self):
        """Test resolving an annotated tag (points to tag object, then commit)."""
        tag_object_sha = "tag123456789"
        with patch("validate.gh_api") as mock_api:
            mock_api.side_effect = [
                {"object": {"sha": tag_object_sha}},  # Tag ref points to tag object
                {"object": {"sha": SAMPLE_COMMIT_SHA}},  # Tag object points to commit
            ]
            commit, error = resolve_tag_to_commit("v1.0.0")
            assert commit == SAMPLE_COMMIT_SHA
            assert error is None


class TestValidateCommit:
    """Tests for validate_commit function."""

    def test_commit_not_found(self):
        with patch("validate.gh_api", return_value=None):
            sha, error = validate_commit("nonexistent")
            assert sha == ""
            assert "does not exist" in error

    def test_commit_found(self):
        with patch("validate.gh_api", return_value={"sha": SAMPLE_COMMIT_SHA}):
            sha, error = validate_commit(SAMPLE_COMMIT_SHA[:7])
            assert sha == SAMPLE_COMMIT_SHA
            assert error is None


class TestFindTagForCommit:
    """Tests for find_tag_for_commit function."""

    def test_no_tags_in_repo(self):
        with patch("validate.gh_api", return_value=None):
            tag, error = find_tag_for_commit(SAMPLE_COMMIT_SHA)
            assert tag == ""
            assert "Failed to fetch tags" in error

    def test_exact_match(self):
        """Test when commit exactly matches a tag."""
        with patch("validate.gh_api") as mock_api:
            mock_api.side_effect = [
                [{"name": "v1.0.0", "commit": {"sha": SAMPLE_COMMIT_SHA}}],
            ]
            tag, error = find_tag_for_commit(SAMPLE_COMMIT_SHA)
            assert tag == "v1.0.0"
            assert error is None

    def test_commit_ahead_of_tag(self):
        """Test when commit is ahead of a tag."""
        with patch("validate.gh_api") as mock_api:
            mock_api.side_effect = [
                [{"name": "v1.0.0", "commit": {"sha": SAMPLE_TAG_COMMIT_SHA}}],
                {"status": "ahead"},  # compare API result
            ]
            tag, error = find_tag_for_commit(SAMPLE_COMMIT_SHA)
            assert tag == "v1.0.0"
            assert error is None

    def test_no_matching_tag(self):
        """Test when no tag is an ancestor of the commit."""
        with patch("validate.gh_api") as mock_api:
            mock_api.side_effect = [
                [{"name": "v1.0.0", "commit": {"sha": SAMPLE_TAG_COMMIT_SHA}}],
                {"status": "diverged"},  # compare API result
            ]
            tag, error = find_tag_for_commit(SAMPLE_COMMIT_SHA)
            assert tag == ""
            assert "No tag found" in error


class TestResolveTagAndCommit:
    """Tests for resolve_tag_and_commit function."""

    def test_neither_provided(self):
        tag, commit, error = resolve_tag_and_commit(None, None)
        assert error == "Either tag or commit_hash must be provided"

    def test_tag_only_success(self):
        """Test with only tag provided."""
        with patch("validate.resolve_tag_to_commit", return_value=(SAMPLE_COMMIT_SHA, None)):
            tag, commit, error = resolve_tag_and_commit("v1.0.0", None)
            assert tag == "v1.0.0"
            assert commit == SAMPLE_COMMIT_SHA
            assert error is None

    def test_tag_only_not_found(self):
        """Test with only tag provided but tag doesn't exist."""
        with patch("validate.resolve_tag_to_commit", return_value=("", "Tag not found")):
            tag, commit, error = resolve_tag_and_commit("v1.0.0", None)
            assert error == "Tag not found"

    def test_commit_only_success(self):
        """Test with only commit provided."""
        with patch("validate.validate_commit", return_value=(SAMPLE_COMMIT_SHA, None)):
            with patch("validate.find_tag_for_commit", return_value=("v1.0.0", None)):
                tag, commit, error = resolve_tag_and_commit(None, SAMPLE_COMMIT_SHA)
                assert tag == "v1.0.0"
                assert commit == SAMPLE_COMMIT_SHA
                assert error is None

    def test_commit_only_no_tag_found(self):
        """Test with only commit provided but no tag found."""
        with patch("validate.validate_commit", return_value=(SAMPLE_COMMIT_SHA, None)):
            with patch("validate.find_tag_for_commit", return_value=("", "No tag found")):
                tag, commit, error = resolve_tag_and_commit(None, SAMPLE_COMMIT_SHA)
                assert "No tag found" in error

    def test_both_provided_commit_at_tag(self):
        """Test with both provided and commit is at the tag."""
        with patch("validate.validate_commit", return_value=(SAMPLE_COMMIT_SHA, None)):
            with patch("validate.resolve_tag_to_commit", return_value=(SAMPLE_COMMIT_SHA, None)):
                tag, commit, error = resolve_tag_and_commit("v1.0.0", SAMPLE_COMMIT_SHA)
                assert tag == "v1.0.0"
                assert commit == SAMPLE_COMMIT_SHA
                assert error is None

    def test_both_provided_commit_ahead_of_tag(self):
        """Test with both provided and commit is ahead of tag."""
        with patch("validate.validate_commit", return_value=(SAMPLE_COMMIT_SHA, None)):
            with patch("validate.resolve_tag_to_commit", return_value=(SAMPLE_TAG_COMMIT_SHA, None)):
                with patch("validate.gh_api", return_value={"status": "ahead"}):
                    tag, commit, error = resolve_tag_and_commit("v1.0.0", SAMPLE_COMMIT_SHA)
                    assert tag == "v1.0.0"
                    assert commit == SAMPLE_COMMIT_SHA
                    assert error is None

    def test_both_provided_commit_behind_tag(self):
        """Test with both provided but commit is behind tag (invalid)."""
        with patch("validate.validate_commit", return_value=(SAMPLE_COMMIT_SHA, None)):
            with patch("validate.resolve_tag_to_commit", return_value=(SAMPLE_TAG_COMMIT_SHA, None)):
                with patch("validate.gh_api", return_value={"status": "behind"}):
                    tag, commit, error = resolve_tag_and_commit("v1.0.0", SAMPLE_COMMIT_SHA)
                    assert "not at or after tag" in error

    def test_both_provided_commit_diverged(self):
        """Test with both provided but commit is on different branch (invalid)."""
        with patch("validate.validate_commit", return_value=(SAMPLE_COMMIT_SHA, None)):
            with patch("validate.resolve_tag_to_commit", return_value=(SAMPLE_TAG_COMMIT_SHA, None)):
                with patch("validate.gh_api", return_value={"status": "diverged"}):
                    tag, commit, error = resolve_tag_and_commit("v1.0.0", SAMPLE_COMMIT_SHA)
                    assert "not at or after tag" in error
                    assert "diverged" in error


class TestCheckCIStatus:
    """Tests for check_ci_status function."""

    def test_ci_success_via_status_api(self):
        """Test CI success detected via status API."""
        with patch("validate.gh_api") as mock_api:
            mock_api.side_effect = [
                {"state": "success", "total_count": 1},
                {"total_count": 0, "check_runs": []},
            ]
            success, message = check_ci_status(SAMPLE_COMMIT_SHA)
            assert success is True
            assert "passed" in message

    def test_ci_success_via_check_runs(self):
        """Test CI success detected via check-runs API."""
        with patch("validate.gh_api") as mock_api:
            mock_api.side_effect = [
                {"state": "unknown", "total_count": 0},
                {
                    "total_count": 2,
                    "check_runs": [
                        {"status": "completed", "conclusion": "success"},
                        {"status": "completed", "conclusion": "success"},
                    ],
                },
            ]
            success, message = check_ci_status(SAMPLE_COMMIT_SHA)
            assert success is True

    def test_ci_failure_via_status_api(self):
        """Test CI failure detected via status API."""
        with patch("validate.gh_api") as mock_api:
            mock_api.side_effect = [
                {"state": "failure", "total_count": 1},
                {"total_count": 0, "check_runs": []},
            ]
            success, message = check_ci_status(SAMPLE_COMMIT_SHA)
            assert success is False
            assert "failed" in message

    def test_ci_failure_via_check_runs(self):
        """Test CI failure detected via check-runs API."""
        with patch("validate.gh_api") as mock_api:
            mock_api.side_effect = [
                {"state": "unknown", "total_count": 0},
                {
                    "total_count": 2,
                    "check_runs": [
                        {"status": "completed", "conclusion": "success"},
                        {"status": "completed", "conclusion": "failure"},
                    ],
                },
            ]
            success, message = check_ci_status(SAMPLE_COMMIT_SHA)
            assert success is False

    def test_ci_pending_via_status_api(self):
        """Test CI pending detected via status API."""
        with patch("validate.gh_api") as mock_api:
            mock_api.side_effect = [
                {"state": "pending", "total_count": 1},
                {"total_count": 0, "check_runs": []},
            ]
            success, message = check_ci_status(SAMPLE_COMMIT_SHA)
            assert success is False
            assert "still running" in message

    def test_ci_pending_via_check_runs(self):
        """Test CI pending detected via check-runs API."""
        with patch("validate.gh_api") as mock_api:
            mock_api.side_effect = [
                {"state": "unknown", "total_count": 0},
                {
                    "total_count": 1,
                    "check_runs": [
                        {"status": "in_progress", "conclusion": None},
                    ],
                },
            ]
            success, message = check_ci_status(SAMPLE_COMMIT_SHA)
            assert success is False
            assert "still running" in message

    def test_ci_no_checks_found(self):
        """Test when no CI checks are found (proceeds with warning)."""
        with patch("validate.gh_api") as mock_api:
            mock_api.side_effect = [
                {"state": "unknown", "total_count": 0},
                {"total_count": 0, "check_runs": []},
            ]
            success, message = check_ci_status(SAMPLE_COMMIT_SHA)
            assert success is True
            assert "No CI checks found" in message

    def test_ci_skipped_checks_count_as_success(self):
        """Test that skipped checks count as success."""
        with patch("validate.gh_api") as mock_api:
            mock_api.side_effect = [
                {"state": "unknown", "total_count": 0},
                {
                    "total_count": 2,
                    "check_runs": [
                        {"status": "completed", "conclusion": "success"},
                        {"status": "completed", "conclusion": "skipped"},
                    ],
                },
            ]
            success, message = check_ci_status(SAMPLE_COMMIT_SHA)
            assert success is True

    def test_ci_ignored_cla_status_does_not_block(self):
        """cla-bot's verification/cla-signed status is permanently red on
        external-contributor backport branches; filtering it out should let
        the build proceed when it's the only failure."""
        with patch("validate.gh_api") as mock_api:
            mock_api.side_effect = [
                {
                    "state": "failure",
                    "total_count": 2,
                    "statuses": [
                        {"context": "verification/cla-signed", "state": "error"},
                        {"context": "buildkite/firecracker", "state": "success"},
                    ],
                },
                {"total_count": 0, "check_runs": []},
            ]
            success, message = check_ci_status(SAMPLE_COMMIT_SHA)
            assert success is True
            assert "passed" in message

    def test_ci_ignored_cla_status_alone_falls_through(self):
        """If the CLA status is the only one and we filter it out, the
        rollup is empty → fall back to the no-checks 'proceed anyway' path."""
        with patch("validate.gh_api") as mock_api:
            mock_api.side_effect = [
                {
                    "state": "failure",
                    "total_count": 1,
                    "statuses": [
                        {"context": "verification/cla-signed", "state": "error"},
                    ],
                },
                {"total_count": 0, "check_runs": []},
            ]
            success, message = check_ci_status(SAMPLE_COMMIT_SHA)
            assert success is True

    def test_ci_other_failure_still_blocks_when_cla_also_failed(self):
        """Real CI failure must still block even when the CLA status is also
        failing — the filter must not mask non-ignored failures."""
        with patch("validate.gh_api") as mock_api:
            mock_api.side_effect = [
                {
                    "state": "failure",
                    "total_count": 2,
                    "statuses": [
                        {"context": "verification/cla-signed", "state": "error"},
                        {"context": "buildkite/firecracker", "state": "failure"},
                    ],
                },
                {"total_count": 0, "check_runs": []},
            ]
            success, message = check_ci_status(SAMPLE_COMMIT_SHA)
            assert success is False
            assert "failed" in message


class TestGenerateBuildMatrix:
    """Tests for generate_build_matrix function."""

    def test_build_both_architectures(self):
        """Test generating matrix for both architectures."""
        matrix = generate_build_matrix(True, True)
        assert len(matrix["include"]) == 2
        archs = {item["arch"] for item in matrix["include"]}
        assert archs == {"amd64", "arm64"}

    def test_build_amd64_only(self):
        """Test generating matrix for amd64 only."""
        matrix = generate_build_matrix(True, False)
        assert len(matrix["include"]) == 1
        assert matrix["include"][0]["arch"] == "amd64"
        assert matrix["include"][0]["runner"] == "ubuntu-24.04"

    def test_build_arm64_only(self):
        """Test generating matrix for arm64 only."""
        matrix = generate_build_matrix(False, True)
        assert len(matrix["include"]) == 1
        assert matrix["include"][0]["arch"] == "arm64"
        assert matrix["include"][0]["runner"] == "ubuntu-24.04-arm"

    def test_build_neither_architecture(self):
        """Test generating empty matrix when no architectures requested."""
        matrix = generate_build_matrix(False, False)
        assert matrix == {"include": []}


class TestGetExistingReleaseAssets:
    """Tests for get_existing_release_assets function."""

    def test_no_github_repository_env(self):
        """Test returns empty set when GITHUB_REPOSITORY is not set."""
        with patch.dict("os.environ", {}, clear=True):
            assets = get_existing_release_assets("v1.0.0_abc1234")
            assert assets == set()

    def test_release_not_found(self):
        """Test returns empty set when release doesn't exist."""
        with patch.dict("os.environ", {"GITHUB_REPOSITORY": "owner/repo"}):
            with patch("validate.run_command") as mock_run:
                mock_run.return_value = MagicMock(returncode=1, stdout="", stderr="release not found")
                assets = get_existing_release_assets("v1.0.0_abc1234")
                assert assets == set()

    def test_release_with_assets(self):
        """Test returns set of asset names when release exists."""
        with patch.dict("os.environ", {"GITHUB_REPOSITORY": "owner/repo"}):
            with patch("validate.run_command") as mock_run:
                mock_run.return_value = MagicMock(
                    returncode=0,
                    stdout="firecracker-amd64\nfirecracker-arm64\nfirecracker\n",
                    stderr=""
                )
                assets = get_existing_release_assets("v1.0.0_abc1234")
                assert assets == {"firecracker-amd64", "firecracker-arm64", "firecracker"}

    def test_release_with_no_assets(self):
        """Test returns empty set when release exists but has no assets."""
        with patch.dict("os.environ", {"GITHUB_REPOSITORY": "owner/repo"}):
            with patch("validate.run_command") as mock_run:
                mock_run.return_value = MagicMock(returncode=0, stdout="", stderr="")
                assets = get_existing_release_assets("v1.0.0_abc1234")
                assert assets == set()


class TestCheckArtifactsNeeded:
    """Tests for check_artifacts_needed function."""

    def test_both_requested_neither_exists(self):
        """Test returns True when both requested and neither exists."""
        with patch("validate.get_existing_release_assets", return_value=set()):
            assert check_artifacts_needed("v1.0.0_abc1234", True, True) is True

    def test_both_requested_amd64_missing(self):
        """Test returns True when both requested but amd64 is missing."""
        with patch("validate.get_existing_release_assets", return_value={"firecracker-arm64"}):
            assert check_artifacts_needed("v1.0.0_abc1234", True, True) is True

    def test_both_requested_arm64_missing(self):
        """Test returns True when both requested but arm64 is missing."""
        with patch("validate.get_existing_release_assets", return_value={"firecracker-amd64"}):
            assert check_artifacts_needed("v1.0.0_abc1234", True, True) is True

    def test_both_requested_both_exist(self):
        """Test returns False when both requested and both prod + debug exist."""
        assets = {
            "firecracker-amd64", "firecracker-arm64",
            "firecracker-debug-amd64", "firecracker-debug-arm64",
        }
        with patch("validate.get_existing_release_assets", return_value=assets):
            assert check_artifacts_needed("v1.0.0_abc1234", True, True) is False

    def test_both_requested_prod_exists_debug_missing(self):
        """Test returns True when prod binaries exist but the debug ones do not."""
        with patch("validate.get_existing_release_assets", return_value={"firecracker-amd64", "firecracker-arm64"}):
            assert check_artifacts_needed("v1.0.0_abc1234", True, True) is True

    def test_amd64_only_exists(self):
        """Test returns False when only amd64 requested and its prod + debug exist."""
        with patch("validate.get_existing_release_assets", return_value={"firecracker-amd64", "firecracker-debug-amd64"}):
            assert check_artifacts_needed("v1.0.0_abc1234", True, False) is False

    def test_amd64_only_prod_exists_debug_missing(self):
        """Test returns True when amd64 prod exists but its debug binary does not."""
        with patch("validate.get_existing_release_assets", return_value={"firecracker-amd64"}):
            assert check_artifacts_needed("v1.0.0_abc1234", True, False) is True

    def test_amd64_only_missing(self):
        """Test returns True when only amd64 requested and it's missing."""
        with patch("validate.get_existing_release_assets", return_value=set()):
            assert check_artifacts_needed("v1.0.0_abc1234", True, False) is True

    def test_arm64_only_exists(self):
        """Test returns False when only arm64 requested and its prod + debug exist."""
        with patch("validate.get_existing_release_assets", return_value={"firecracker-arm64", "firecracker-debug-arm64"}):
            assert check_artifacts_needed("v1.0.0_abc1234", False, True) is False

    def test_arm64_only_missing(self):
        """Test returns True when only arm64 requested and it's missing."""
        with patch("validate.get_existing_release_assets", return_value=set()):
            assert check_artifacts_needed("v1.0.0_abc1234", False, True) is True

    def test_neither_requested(self):
        """Test returns False when no architectures are requested."""
        with patch("validate.get_existing_release_assets", return_value=set()):
            assert check_artifacts_needed("v1.0.0_abc1234", False, False) is False
