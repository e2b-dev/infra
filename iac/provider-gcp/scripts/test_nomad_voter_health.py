#!/usr/bin/env python3
import importlib.util
import json
import os
import pathlib
import tempfile
import threading
import unittest
import urllib.error
import urllib.request
from http.server import HTTPServer
from unittest import mock


SCRIPT_PATH = (
    pathlib.Path(__file__).resolve().parents[1]
    / "nomad-cluster"
    / "scripts"
    / "nomad-voter-health.py"
)
SPEC = importlib.util.spec_from_file_location("nomad_voter_health", SCRIPT_PATH)
HEALTH = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(HEALTH)


def server(index, healthy=True, voter=True, status="alive"):
    return {
        "ID": "server-id-{}".format(index),
        "Name": "e2b-orch-server-{}.us-east4".format(index),
        "Address": "10.150.0.{}:4647".format(10 + index),
        "SerfStatus": status,
        "Healthy": healthy,
        "Voter": voter,
    }


def healthy_payload():
    return {
        "Healthy": True,
        "FailureTolerance": 1,
        "Servers": [server(0), server(1), server(2)],
    }


class TokenFileTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.directory = pathlib.Path(self.temp.name) / "health"
        self.directory.mkdir(mode=0o700)
        os.chmod(self.directory, 0o700)
        self.path = self.directory / "token"
        self.path.write_text("token-sentinel", encoding="ascii")
        os.chmod(self.path, 0o600)
        self.uid = os.getuid()
        self.gid = os.getgid()
        self.patches = [
            mock.patch.object(HEALTH, "TOKEN_DIRECTORY", str(self.directory)),
            mock.patch.object(HEALTH, "TOKEN_PATH", str(self.path)),
        ]
        for patch in self.patches:
            patch.start()

    def tearDown(self):
        for patch in reversed(self.patches):
            patch.stop()
        self.temp.cleanup()

    def load(self):
        return HEALTH.load_token(expected_uid=self.uid, expected_gid=self.gid)

    def test_reads_exact_secure_file(self):
        self.assertEqual(self.load(), "token-sentinel")

    def test_rejects_wrong_directory_mode(self):
        os.chmod(self.directory, 0o755)
        with self.assertRaises(HEALTH.HealthError):
            self.load()

    def test_rejects_wrong_file_mode_or_owner(self):
        os.chmod(self.path, 0o640)
        with self.assertRaises(HEALTH.HealthError):
            self.load()
        os.chmod(self.path, 0o600)
        with self.assertRaises(HEALTH.HealthError):
            HEALTH.load_token(expected_uid=self.uid + 1, expected_gid=self.gid)

    def test_rejects_symlink_and_hardlink(self):
        self.path.unlink()
        target = self.directory / "target"
        target.write_text("token-sentinel", encoding="ascii")
        os.chmod(target, 0o600)
        self.path.symlink_to(target)
        with self.assertRaises(HEALTH.HealthError):
            self.load()
        self.path.unlink()
        os.link(target, self.path)
        with self.assertRaises(HEALTH.HealthError):
            self.load()

    def test_rejects_whitespace_non_ascii_and_oversize(self):
        for value in (b"token\n", "tökën".encode("utf-8"), b"x" * 4097):
            self.path.write_bytes(value)
            os.chmod(self.path, 0o600)
            with self.assertRaises(HEALTH.HealthError):
                self.load()


class AutopilotTests(unittest.TestCase):
    def evaluate(self, payload):
        return HEALTH.evaluate_autopilot(
            payload,
            "e2b-orch-server-0",
            "us-east4-c",
            "10.150.0.10",
        )

    def test_accepts_exact_local_voter_with_quorum_headroom(self):
        self.assertTrue(self.evaluate(healthy_payload()))

    def test_rejects_cluster_unhealthy_or_without_failure_tolerance(self):
        for key, value in (("Healthy", False), ("FailureTolerance", 0)):
            payload = healthy_payload()
            payload[key] = value
            with self.assertRaises(HEALTH.HealthError):
                self.evaluate(payload)

    def test_rejects_fewer_than_three_healthy_voters(self):
        for field, value in (
            ("Healthy", False),
            ("Voter", False),
            ("SerfStatus", "left"),
        ):
            payload = healthy_payload()
            payload["Servers"][2][field] = value
            with self.assertRaises(HEALTH.HealthError):
                self.evaluate(payload)

    def test_rejects_local_node_not_alive_healthy_voter(self):
        for field, value in (
            ("Healthy", False),
            ("Voter", False),
            ("SerfStatus", "failed"),
        ):
            payload = healthy_payload()
            payload["Servers"][0][field] = value
            payload["Servers"].append(server(3))
            with self.assertRaises(HEALTH.HealthError):
                self.evaluate(payload)

    def test_rejects_local_name_or_address_mismatch_and_duplicate_identity(self):
        payload = healthy_payload()
        payload["Servers"][0]["Address"] = "10.150.0.99:4647"
        with self.assertRaises(HEALTH.HealthError):
            self.evaluate(payload)

        payload = healthy_payload()
        payload["Servers"][0]["Name"] = "wrong.us-east4"
        with self.assertRaises(HEALTH.HealthError):
            self.evaluate(payload)

        payload = healthy_payload()
        payload["Servers"][1]["ID"] = payload["Servers"][0]["ID"]
        with self.assertRaises(HEALTH.HealthError):
            self.evaluate(payload)

    def test_rejects_malformed_types(self):
        for key, value in (
            ("Healthy", 1),
            ("FailureTolerance", True),
            ("Servers", {}),
        ):
            payload = healthy_payload()
            payload[key] = value
            with self.assertRaises(HEALTH.HealthError):
                self.evaluate(payload)


class JsonAndProbeTests(unittest.TestCase):
    def test_strict_json_rejects_duplicate_keys_and_non_object(self):
        with self.assertRaises(HEALTH.HealthError):
            HEALTH._parse_json_object(b'{"Healthy":true,"Healthy":false}')
        with self.assertRaises(HEALTH.HealthError):
            HEALTH._parse_json_object(b"[]")

    def test_probe_sends_token_only_as_nomad_header(self):
        payload = json.dumps(healthy_payload()).encode("utf-8")
        calls = []

        def metadata(path):
            return {
                "instance/name": "e2b-orch-server-0",
                "instance/zone": "projects/fixture/zones/us-east4-c",
                "instance/network-interfaces/0/ip": "10.150.0.10",
            }[path]

        def read_url(url, headers, timeout):
            calls.append((url, headers, timeout))
            return payload, {}

        with mock.patch.object(HEALTH, "load_token", return_value="token-sentinel"), mock.patch.object(
            HEALTH, "_metadata", side_effect=metadata
        ), mock.patch.object(HEALTH, "_read_url", side_effect=read_url):
            self.assertTrue(HEALTH.probe())

        self.assertEqual(len(calls), 1)
        url, headers, timeout = calls[0]
        self.assertEqual(url, HEALTH.NOMAD_AUTOPILOT_URL)
        self.assertNotIn("token-sentinel", url)
        self.assertEqual(headers, {"X-Nomad-Token": "token-sentinel"})
        self.assertEqual(timeout, HEALTH.NOMAD_TIMEOUT_SECONDS)

    def test_http_response_never_exposes_probe_error_or_token(self):
        sentinel = "token-must-never-be-returned"
        server_instance = HTTPServer(("127.0.0.1", 0), HEALTH.HealthHandler)
        thread = threading.Thread(target=server_instance.handle_request)
        thread.start()
        try:
            with mock.patch.object(
                HEALTH,
                "probe",
                side_effect=HEALTH.HealthError(sentinel),
            ):
                request = urllib.request.Request(
                    "http://127.0.0.1:{}/healthz".format(server_instance.server_port)
                )
                try:
                    urllib.request.urlopen(request, timeout=2)
                    self.fail("unhealthy probe unexpectedly returned 200")
                except urllib.error.HTTPError as exc:
                    with exc:
                        body = exc.read()
                        self.assertEqual(exc.code, 503)
                        self.assertEqual(body, b'{"ok":false}\n')
                        self.assertNotIn(sentinel.encode("ascii"), body)
        finally:
            thread.join(timeout=2)
            server_instance.server_close()
        self.assertFalse(thread.is_alive())


if __name__ == "__main__":
    unittest.main()
