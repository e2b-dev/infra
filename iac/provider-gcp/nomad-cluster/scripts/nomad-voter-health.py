#!/usr/bin/env python3
"""Fail-closed GCE health endpoint for a local Nomad server voter.

The response intentionally contains no diagnostic details. Operators get the
underlying Nomad and MIG evidence through their normal authenticated paths; the
GCE health checker only learns whether this exact instance is safe to count as
ready during a server rollout.
"""

import ipaddress
import json
import os
import re
import stat
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, HTTPServer


LISTEN_ADDRESS = "0.0.0.0"
LISTEN_PORT = 50001
HEALTH_PATH = "/healthz"
TOKEN_DIRECTORY = "/run/e2b-nomad-health"
TOKEN_PATH = TOKEN_DIRECTORY + "/token"
METADATA_BASE_URL = "http://metadata.google.internal/computeMetadata/v1/"
NOMAD_AUTOPILOT_URL = "http://127.0.0.1:4646/v1/operator/autopilot/health"
METADATA_TIMEOUT_SECONDS = 0.5
NOMAD_TIMEOUT_SECONDS = 1.5
MAX_TOKEN_BYTES = 4096
MAX_RESPONSE_BYTES = 65536
INSTANCE_NAME_RE = re.compile(r"^[a-z](?:[-a-z0-9]{0,61}[a-z0-9])?$")
ZONE_RE = re.compile(r"^(?P<region>[a-z0-9-]+)-[a-z]$")


class HealthError(Exception):
    """Expected fail-closed probe error; never returned to the caller."""


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        del req, fp, code, msg, headers, newurl
        return None


URL_OPENER = urllib.request.build_opener(
    urllib.request.ProxyHandler({}),
    _NoRedirect(),
)


def _mode(value):
    return stat.S_IMODE(value.st_mode)


def load_token(path=None, expected_uid=0, expected_gid=0):
    """Read a bounded token through no-follow descriptors after exact checks."""

    if path is None:
        path = TOKEN_PATH
    directory = os.path.dirname(path)
    basename = os.path.basename(path)
    if not basename or path != TOKEN_PATH or directory != TOKEN_DIRECTORY:
        raise HealthError("invalid token path")

    try:
        directory_lstat = os.lstat(directory)
        if not stat.S_ISDIR(directory_lstat.st_mode):
            raise HealthError("token directory is not a directory")
        if stat.S_ISLNK(directory_lstat.st_mode):
            raise HealthError("token directory is a symlink")
        if (
            directory_lstat.st_uid != expected_uid
            or directory_lstat.st_gid != expected_gid
            or _mode(directory_lstat) != 0o700
        ):
            raise HealthError("token directory ownership or mode is unsafe")

        directory_fd = os.open(
            directory,
            os.O_RDONLY | os.O_DIRECTORY | getattr(os, "O_NOFOLLOW", 0),
        )
    except (OSError, ValueError) as exc:
        raise HealthError("cannot open token directory") from exc

    try:
        directory_fstat = os.fstat(directory_fd)
        if (
            directory_fstat.st_dev != directory_lstat.st_dev
            or directory_fstat.st_ino != directory_lstat.st_ino
            or directory_fstat.st_uid != expected_uid
            or directory_fstat.st_gid != expected_gid
            or _mode(directory_fstat) != 0o700
            or not stat.S_ISDIR(directory_fstat.st_mode)
        ):
            raise HealthError("token directory changed during validation")

        try:
            file_lstat = os.stat(basename, dir_fd=directory_fd, follow_symlinks=False)
            if stat.S_ISLNK(file_lstat.st_mode) or not stat.S_ISREG(file_lstat.st_mode):
                raise HealthError("token path is not a regular file")
            file_fd = os.open(
                basename,
                os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0),
                dir_fd=directory_fd,
            )
        except (OSError, ValueError) as exc:
            raise HealthError("cannot open token file") from exc

        try:
            file_fstat = os.fstat(file_fd)
            if (
                file_fstat.st_dev != file_lstat.st_dev
                or file_fstat.st_ino != file_lstat.st_ino
                or not stat.S_ISREG(file_fstat.st_mode)
                or file_fstat.st_uid != expected_uid
                or file_fstat.st_gid != expected_gid
                or _mode(file_fstat) != 0o600
                or file_fstat.st_nlink != 1
                or file_fstat.st_size < 1
                or file_fstat.st_size > MAX_TOKEN_BYTES
            ):
                raise HealthError("token file ownership, mode, or size is unsafe")
            token_bytes = os.read(file_fd, MAX_TOKEN_BYTES + 1)
            if len(token_bytes) != file_fstat.st_size or len(token_bytes) > MAX_TOKEN_BYTES:
                raise HealthError("token file changed during read")
        finally:
            os.close(file_fd)
    finally:
        os.close(directory_fd)

    try:
        token = token_bytes.decode("ascii")
    except UnicodeDecodeError as exc:
        raise HealthError("token is not ASCII") from exc
    if not token or any(character.isspace() for character in token) or "\x00" in token:
        raise HealthError("token contains forbidden characters")
    return token


def _read_url(url, headers, timeout):
    request = urllib.request.Request(url, headers=headers, method="GET")
    try:
        with URL_OPENER.open(request, timeout=timeout) as response:
            if response.getcode() != 200:
                raise HealthError("upstream returned non-200")
            raw = response.read(MAX_RESPONSE_BYTES + 1)
    except (OSError, urllib.error.URLError, ValueError) as exc:
        raise HealthError("upstream request failed") from exc
    if len(raw) > MAX_RESPONSE_BYTES:
        raise HealthError("upstream response is too large")
    return raw, response.headers


def _metadata(path):
    raw, headers = _read_url(
        METADATA_BASE_URL + path,
        {"Metadata-Flavor": "Google"},
        METADATA_TIMEOUT_SECONDS,
    )
    if headers.get("Metadata-Flavor") != "Google":
        raise HealthError("metadata response is unauthenticated")
    try:
        value = raw.decode("ascii")
    except UnicodeDecodeError as exc:
        raise HealthError("metadata value is not ASCII") from exc
    if not value or value != value.strip() or any(character.isspace() for character in value):
        raise HealthError("metadata value is malformed")
    return value


def _strict_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise HealthError("duplicate JSON key")
        result[key] = value
    return result


def _parse_json_object(raw):
    try:
        parsed = json.loads(raw.decode("utf-8"), object_pairs_hook=_strict_object)
    except (UnicodeDecodeError, json.JSONDecodeError, HealthError) as exc:
        raise HealthError("invalid JSON response") from exc
    if not isinstance(parsed, dict):
        raise HealthError("JSON response is not an object")
    return parsed


def evaluate_autopilot(payload, instance_name, zone, private_ip):
    """Require cluster quorum and this exact GCE instance as a healthy voter."""

    if not INSTANCE_NAME_RE.fullmatch(instance_name):
        raise HealthError("invalid instance name")
    zone_match = ZONE_RE.fullmatch(zone)
    if zone_match is None:
        raise HealthError("invalid zone")
    try:
        address = ipaddress.ip_address(private_ip)
    except ValueError as exc:
        raise HealthError("invalid private IP") from exc
    if address.version != 4 or address.is_unspecified or address.is_loopback:
        raise HealthError("invalid private IP")

    if payload.get("Healthy") is not True:
        raise HealthError("cluster is unhealthy")
    failure_tolerance = payload.get("FailureTolerance")
    if (
        isinstance(failure_tolerance, bool)
        or not isinstance(failure_tolerance, int)
        or failure_tolerance < 1
    ):
        raise HealthError("cluster has no failure tolerance")
    servers = payload.get("Servers")
    if not isinstance(servers, list):
        raise HealthError("server list is malformed")

    expected_name = instance_name + "." + zone_match.group("region")
    expected_address = private_ip + ":4647"
    local_candidates = []
    healthy_voters = 0
    seen_ids = set()
    seen_names = set()
    seen_addresses = set()
    for server in servers:
        if not isinstance(server, dict):
            raise HealthError("server entry is malformed")
        server_id = server.get("ID")
        name = server.get("Name")
        server_address = server.get("Address")
        serf_status = server.get("SerfStatus")
        healthy = server.get("Healthy")
        voter = server.get("Voter")
        if (
            not isinstance(server_id, str)
            or not server_id
            or not isinstance(name, str)
            or not name
            or not isinstance(server_address, str)
            or not server_address
            or not isinstance(serf_status, str)
            or type(healthy) is not bool
            or type(voter) is not bool
        ):
            raise HealthError("server entry has invalid fields")
        if server_id in seen_ids or name in seen_names or server_address in seen_addresses:
            raise HealthError("server list contains duplicate identity")
        seen_ids.add(server_id)
        seen_names.add(name)
        seen_addresses.add(server_address)
        if serf_status == "alive" and healthy is True and voter is True:
            healthy_voters += 1
        if name == expected_name or server_address == expected_address:
            local_candidates.append(server)

    if healthy_voters < 3:
        raise HealthError("fewer than three healthy voters")
    if len(local_candidates) != 1:
        raise HealthError("local voter identity is missing or ambiguous")
    local = local_candidates[0]
    if (
        local.get("Name") != expected_name
        or local.get("Address") != expected_address
        or local.get("SerfStatus") != "alive"
        or local.get("Healthy") is not True
        or local.get("Voter") is not True
    ):
        raise HealthError("local server is not an exact healthy voter")
    return True


def probe():
    token = load_token()
    instance_name = _metadata("instance/name")
    zone_value = _metadata("instance/zone")
    zone = zone_value.rsplit("/", 1)[-1]
    private_ip = _metadata("instance/network-interfaces/0/ip")
    raw, _headers = _read_url(
        NOMAD_AUTOPILOT_URL,
        {"X-Nomad-Token": token},
        NOMAD_TIMEOUT_SECONDS,
    )
    return evaluate_autopilot(
        _parse_json_object(raw),
        instance_name,
        zone,
        private_ip,
    )


class HealthHandler(BaseHTTPRequestHandler):
    server_version = "e2b-nomad-health"
    sys_version = ""

    def log_message(self, format_string, *args):
        del format_string, args

    def _respond(self, include_body):
        if self.path != HEALTH_PATH:
            status = 404
            body = b'{"ok":false}\n'
        else:
            try:
                healthy = probe()
            except Exception:  # Fail closed without returning secrets/details.
                healthy = False
            status = 200 if healthy else 503
            body = b'{"ok":true}\n' if healthy else b'{"ok":false}\n'
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Cache-Control", "no-store")
        self.send_header("Content-Length", str(len(body) if include_body else 0))
        self.end_headers()
        if include_body:
            self.wfile.write(body)

    def do_GET(self):
        self._respond(True)

    def do_HEAD(self):
        self._respond(False)


def main():
    server = HTTPServer((LISTEN_ADDRESS, LISTEN_PORT), HealthHandler)
    server.serve_forever()


if __name__ == "__main__":
    main()
