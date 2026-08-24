"""Tests for upload-to-gcs.sh, driven through a stub gcloud on PATH."""

import os
import stat
import subprocess
from pathlib import Path

import pytest

SCRIPT = Path(__file__).resolve().parent / "upload-to-gcs.sh"
HASH = "c1a568c9f0e1d2b3a4c5d6e7f8091a2b3c4d5e6f"
SHORT = HASH[:7]


@pytest.fixture
def builds(tmp_path):
    """What build.sh leaves behind, for two versions."""
    root = tmp_path / "builds"
    for version, arches in (("6.1.158", ("amd64", "arm64")), ("6.1.177", ("amd64",))):
        for arch in arches:
            directory = root / f"vmlinux-{version}" / arch
            directory.mkdir(parents=True)
            (directory / "vmlinux.bin").write_text(f"{version} {arch} boot", encoding="utf-8")
    return root


@pytest.fixture
def gcloud(tmp_path):
    """A gcloud that finds nothing in the bucket and logs what it is asked."""
    log = tmp_path / "gcloud.log"
    stub = tmp_path / "bin" / "gcloud"
    stub.parent.mkdir(parents=True)
    stub.write_text(
        "#!/usr/bin/env bash\n"
        f'printf "%s\\n" "$*" >> "{log}"\n'
        # `ls` is the "does this object exist" probe.
        'if [[ "$1 $2" == "storage ls" ]]; then exit 1; fi\n'
        "exit 0\n",
        encoding="utf-8",
    )
    stub.chmod(stub.stat().st_mode | stat.S_IEXEC)
    return log, str(stub.parent)


def run(gcloud, *args, expect: int = 0) -> subprocess.CompletedProcess:
    log, bindir = gcloud
    result = subprocess.run(
        ["bash", str(SCRIPT), *args],
        capture_output=True,
        text=True,
        env={**os.environ, "PATH": f"{bindir}:{os.environ['PATH']}"},
        check=False,
    )
    assert result.returncode == expect, result.stderr
    return result


def uploads(gcloud) -> list[tuple[str, str]]:
    log, _ = gcloud
    if not log.exists():
        return []
    return [
        (parts[2], parts[3])
        for parts in (line.split() for line in log.read_text(encoding="utf-8").splitlines())
        if parts[:2] == ["storage", "cp"]
    ]


class TestTheObjectLayout:
    def test_names_each_object_for_its_version_commit_and_arch(self, builds, gcloud):
        run(gcloud, "--builds", str(builds), "--hash", HASH, "--deployment", "public")

        assert {dst for _, dst in uploads(gcloud)} == {
            f"gs://e2b-artifact-binaries/kernels/vmlinux-6.1.158_{SHORT}/amd64/vmlinux.bin",
            f"gs://e2b-artifact-binaries/kernels/vmlinux-6.1.158_{SHORT}/arm64/vmlinux.bin",
            f"gs://e2b-artifact-binaries/kernels/vmlinux-6.1.177_{SHORT}/amd64/vmlinux.bin",
        }

    def test_carries_the_debug_companion_beside_the_boot_image(self, builds, gcloud):
        # The only copy of the symbols: the boot image is stripped of them.
        (builds / "vmlinux-6.1.177" / "amd64" / "vmlinux.debug").write_text("dwarf")

        run(gcloud, "--builds", str(builds), "--hash", HASH, "--deployment", "public")

        assert (
            f"gs://e2b-artifact-binaries/kernels/vmlinux-6.1.177_{SHORT}/amd64/vmlinux.debug"
            in {dst for _, dst in uploads(gcloud)}
        )

    def test_skips_the_legacy_boot_image_outside_an_arch_directory(self, builds, gcloud):
        # Nothing consumes a flat layout under a hash-suffixed name.
        flat = builds / "vmlinux-6.1.177" / "vmlinux.bin"
        flat.write_text("legacy", encoding="utf-8")

        run(gcloud, "--builds", str(builds), "--hash", HASH, "--deployment", "public")

        assert str(flat) not in {src for src, _ in uploads(gcloud)}
        assert len(uploads(gcloud)) == 3


class TestTheDestination:
    def test_a_cluster_reads_its_bucket_root_from_the_environment(self, builds, gcloud):
        # Cluster bucket names are not public, so this script never holds them.
        log, bindir = gcloud
        subprocess.run(
            ["bash", str(SCRIPT), "--builds", str(builds), "--hash", HASH,
             "--deployment", "somewhere"],
            capture_output=True, text=True, check=True,
            env={**os.environ, "PATH": f"{bindir}:{os.environ['PATH']}",
                 "FC_CLUSTER_BUCKET_ROOT": "gs://a-bucket/"},
        )

        assert all(dst.startswith("gs://a-bucket/vmlinux-") for _, dst in uploads(gcloud))

    def test_refuses_a_cluster_with_no_bucket_root(self, builds, gcloud):
        result = run(
            gcloud, "--builds", str(builds), "--hash", HASH,
            "--deployment", "somewhere", expect=1,
        )

        assert "FC_CLUSTER_BUCKET_ROOT" in result.stderr


class TestRefusals:
    def test_an_empty_build_tree_fails_rather_than_publishing_nothing(self, tmp_path, gcloud):
        # Succeeding here would mark a version published with no bytes in the
        # bucket, and the publish matrix would skip it from then on.
        empty = tmp_path / "builds"
        empty.mkdir()

        result = run(gcloud, "--builds", str(empty), "--hash", HASH,
                     "--deployment", "public", expect=1)

        assert "no vmlinux-" in result.stderr

    def test_a_missing_build_tree_fails(self, tmp_path, gcloud):
        result = run(gcloud, "--builds", str(tmp_path / "nope"), "--hash", HASH,
                     "--deployment", "public", expect=1)

        assert "no build tree" in result.stderr

    def test_something_that_is_not_a_commit_hash_fails(self, builds, gcloud):
        # The hash names the object, so a wrong one is a wrong publish.
        result = run(gcloud, "--builds", str(builds), "--hash", "HEAD",
                     "--deployment", "public", expect=1)

        assert "not a commit hash" in result.stderr


class TestReRuns:
    def test_an_object_already_in_the_bucket_is_left_alone(self, builds, tmp_path):
        # The publish account cannot replace an object, so an overwrite would
        # fail the run rather than update anything.
        log = tmp_path / "gcloud.log"
        stub = tmp_path / "bin" / "gcloud"
        stub.parent.mkdir(parents=True)
        stub.write_text(
            "#!/usr/bin/env bash\n"
            f'printf "%s\\n" "$*" >> "{log}"\n'
            'if [[ "$1 $2" == "storage ls" ]]; then exit 0; fi\n'
            "exit 0\n",
            encoding="utf-8",
        )
        stub.chmod(stub.stat().st_mode | stat.S_IEXEC)

        result = run((log, str(stub.parent)), "--builds", str(builds), "--hash", HASH,
                     "--deployment", "public")

        assert uploads((log, str(stub.parent))) == []
        assert "Uploaded: 0, already in GCS: 3" in result.stdout

    def test_a_dry_run_writes_nothing(self, builds, gcloud):
        result = run(gcloud, "--builds", str(builds), "--hash", HASH,
                     "--deployment", "public", "--dry-run")

        assert uploads(gcloud) == []
        assert "WOULD" in result.stdout
