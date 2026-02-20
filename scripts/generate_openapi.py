#!/usr/bin/env python3
"""Generate a merged OpenAPI spec for the full E2B developer-facing API.

Combines multiple sources into a single e2b-openapi.yml:

  Sandbox API (served on <port>-<sandboxID>.e2b.app):
    - Proto-generated OpenAPI for process/filesystem Connect RPC
    - Hand-written REST spec (packages/envd/spec/envd.yaml)
    - Auto-generated stubs for streaming RPCs (parsed from .proto files)

  Platform API (served on api.e2b.app):
    - Main E2B API spec (spec/openapi.yml)

Usage:
    python3 scripts/generate-openapi/envd.py

Outputs e2b-openapi.yml in the current working directory.
Requires: Docker, PyYAML (pip install pyyaml).
"""

from __future__ import annotations

import os
import re
import subprocess
import sys
import tempfile
from dataclasses import dataclass
from glob import glob
from typing import Any

import yaml

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
REPO_ROOT = os.path.abspath(os.path.join(SCRIPT_DIR, ".."))

# Sandbox (envd) specs
ENVD_SPEC_DIR = os.path.join(REPO_ROOT, "packages/envd/spec")
ENVD_REST_SPEC = os.path.join(ENVD_SPEC_DIR, "envd.yaml")

# Platform API specs
API_SPEC = os.path.join(REPO_ROOT, "spec/openapi.yml")

DOCKER_IMAGE = "protoc-gen-connect-openapi"

DOCKERFILE = """\
FROM golang:1.25-alpine
RUN apk add --no-cache git
RUN go install github.com/bufbuild/buf/cmd/buf@v1.50.0
RUN go install github.com/sudorandom/protoc-gen-connect-openapi@latest
ENV PATH="/go/bin:${PATH}"
"""

BUF_GEN_YAML = """\
version: v1
plugins:
  - plugin: connect-openapi
    out: /output
    opt:
      - format=yaml
"""

# Server definitions for the two API surfaces
SANDBOX_SERVER = {
    "url": "https://{port}-{sandboxID}.e2b.app",
    "description": "Sandbox API (envd) — runs inside each sandbox",
    "variables": {
        "port": {"default": "49983", "description": "Port number"},
        "sandboxID": {"default": "{sandbox-id}", "description": "Sandbox identifier"},
    },
}

PLATFORM_SERVER = {
    "url": "https://api.e2b.app",
    "description": "E2B Platform API",
}

# Tag used to mark sandbox-specific paths so we can attach the right server
SANDBOX_TAG = "x-e2b-server"

# Security scheme name for envd endpoints (must not collide with platform's AccessTokenAuth)
SANDBOX_AUTH_SCHEME = "SandboxAccessTokenAuth"

# ---------------------------------------------------------------------------
# Proto parsing — auto-detect streaming RPCs
# ---------------------------------------------------------------------------

@dataclass
class RpcMethod:
    """An RPC method parsed from a .proto file."""

    package: str
    service: str
    method: str
    request_type: str
    response_type: str
    client_streaming: bool
    server_streaming: bool
    comment: str

    @property
    def path(self) -> str:
        return f"/{self.package}.{self.service}/{self.method}"

    @property
    def tag(self) -> str:
        return f"{self.package}.{self.service}"

    @property
    def operation_id(self) -> str:
        return f"{self.package}.{self.service}.{self.method}"

    @property
    def request_schema_ref(self) -> str:
        return f"#/components/schemas/{self.package}.{self.request_type}"

    @property
    def response_schema_ref(self) -> str:
        return f"#/components/schemas/{self.package}.{self.response_type}"

    @property
    def is_streaming(self) -> bool:
        return self.client_streaming or self.server_streaming

    @property
    def streaming_label(self) -> str:
        if self.client_streaming and self.server_streaming:
            return "Bidirectional-streaming"
        if self.client_streaming:
            return "Client-streaming"
        if self.server_streaming:
            return "Server-streaming"
        return "Unary"


_PACKAGE_RE = re.compile(r"^package\s+(\w+)\s*;", re.MULTILINE)
_SERVICE_RE = re.compile(r"service\s+(\w+)\s*\{", re.MULTILINE)
_RPC_RE = re.compile(
    r"rpc\s+(\w+)\s*\(\s*(stream\s+)?(\w+)\s*\)\s*returns\s*\(\s*(stream\s+)?(\w+)\s*\)"
)


def parse_proto_file(path: str) -> list[RpcMethod]:
    """Parse a .proto file and return all RPC methods found."""
    with open(path) as f:
        content = f.read()

    pkg_match = _PACKAGE_RE.search(content)
    if not pkg_match:
        return []
    package = pkg_match.group(1)

    methods: list[RpcMethod] = []

    for svc_match in _SERVICE_RE.finditer(content):
        service_name = svc_match.group(1)
        brace_start = content.index("{", svc_match.start())
        depth, pos = 1, brace_start + 1
        while depth > 0 and pos < len(content):
            if content[pos] == "{":
                depth += 1
            elif content[pos] == "}":
                depth -= 1
            pos += 1
        service_body = content[brace_start:pos]

        for rpc_match in _RPC_RE.finditer(service_body):
            rpc_start = service_body.rfind("\n", 0, rpc_match.start())
            comment = _extract_comment(service_body, rpc_start)

            methods.append(RpcMethod(
                package=package,
                service=service_name,
                method=rpc_match.group(1),
                request_type=rpc_match.group(3),
                response_type=rpc_match.group(5),
                client_streaming=bool(rpc_match.group(2)),
                server_streaming=bool(rpc_match.group(4)),
                comment=comment,
            ))

    return methods


def _extract_comment(text: str, before_pos: int) -> str:
    """Extract // comment lines immediately above a position in text."""
    lines = text[:before_pos].rstrip().split("\n")
    comment_lines: list[str] = []
    for line in reversed(lines):
        stripped = line.strip()
        if stripped.startswith("//"):
            comment_lines.append(stripped.lstrip("/ "))
        elif stripped == "":
            continue
        else:
            break
    comment_lines.reverse()
    return " ".join(comment_lines)


def find_streaming_rpcs(spec_dir: str) -> list[RpcMethod]:
    """Scan all .proto files under spec_dir and return streaming RPCs."""
    streaming: list[RpcMethod] = []
    for proto_path in sorted(glob(os.path.join(spec_dir, "**/*.proto"), recursive=True)):
        for rpc in parse_proto_file(proto_path):
            if rpc.is_streaming:
                streaming.append(rpc)
    return streaming


def build_streaming_path(rpc: RpcMethod) -> dict[str, Any]:
    """Build an OpenAPI path item for a streaming RPC."""
    description = (
        f"{rpc.streaming_label} RPC. "
        f"{rpc.comment + '. ' if rpc.comment else ''}"
        f"Use the Connect protocol with streaming support."
    )
    return {
        "post": {
            "tags": [rpc.tag],
            "summary": rpc.method,
            "description": description,
            "operationId": rpc.operation_id,
            "requestBody": {
                "content": {
                    "application/json": {
                        "schema": {"$ref": rpc.request_schema_ref}
                    }
                },
                "required": True,
            },
            "responses": {
                "200": {
                    "description": f"Stream of {rpc.response_type} events",
                    "content": {
                        "application/json": {
                            "schema": {"$ref": rpc.response_schema_ref}
                        }
                    },
                },
            },
        }
    }


# ---------------------------------------------------------------------------
# Docker build & proto generation
# ---------------------------------------------------------------------------

def docker_build_image() -> None:
    """Build the Docker image with buf + protoc-gen-connect-openapi."""
    print("==> Building Docker image")
    with tempfile.NamedTemporaryFile(mode="w", suffix=".Dockerfile", delete=False) as f:
        f.write(DOCKERFILE)
        f.flush()
        subprocess.run(
            ["docker", "build", "-t", DOCKER_IMAGE, "-f", f.name, "."],
            check=True,
            cwd=REPO_ROOT,
        )
    os.unlink(f.name)


def docker_generate_specs() -> list[str]:
    """Run buf generate inside Docker, return list of generated YAML strings."""
    print("==> Generating OpenAPI specs from proto files")
    with tempfile.TemporaryDirectory() as tmpdir:
        buf_gen_path = os.path.join(tmpdir, "buf.gen.yaml")
        with open(buf_gen_path, "w") as f:
            f.write(BUF_GEN_YAML)

        output_dir = os.path.join(tmpdir, "output")
        os.makedirs(output_dir)

        subprocess.run(
            [
                "docker", "run", "--rm",
                "-v", f"{ENVD_SPEC_DIR}:/spec:ro",
                "-v", f"{buf_gen_path}:/config/buf.gen.yaml:ro",
                "-v", f"{output_dir}:/output",
                DOCKER_IMAGE,
                "sh", "-c",
                "cd /spec && buf generate --template /config/buf.gen.yaml",
            ],
            check=True,
        )

        generated: list[str] = []
        for root, _, files in os.walk(output_dir):
            for name in sorted(files):
                if name.endswith((".yaml", ".yml")):
                    path = os.path.join(root, name)
                    rel = os.path.relpath(path, output_dir)
                    print(f"    Generated: {rel}")
                    with open(path) as f:
                        generated.append(f.read())

        if not generated:
            print("ERROR: No files were generated", file=sys.stderr)
            sys.exit(1)

        return generated


# ---------------------------------------------------------------------------
# OpenAPI merging & post-processing
# ---------------------------------------------------------------------------

def load_yaml_file(path: str) -> str:
    """Load a YAML file and return its raw content."""
    print(f"==> Loading spec: {os.path.relpath(path, REPO_ROOT)}")
    with open(path) as f:
        return f.read()


def merge_specs(raw_docs: list[str]) -> dict[str, Any]:
    """Merge multiple raw YAML OpenAPI docs into a single spec."""
    merged: dict[str, Any] = {
        "openapi": "3.1.0",
        "info": {
            "title": "E2B API",
            "version": "0.1.0",
            "description": (
                "Complete E2B developer API. "
                "Platform endpoints are served on api.e2b.app. "
                "Sandbox endpoints (envd) are served on {port}-{sandboxID}.e2b.app."
            ),
        },
        "servers": [PLATFORM_SERVER],
        "paths": {},
        "components": {},
    }

    for raw in raw_docs:
        doc = yaml.safe_load(raw)
        if not doc:
            continue

        for path, methods in doc.get("paths", {}).items():
            merged["paths"][path] = methods

        for section, entries in doc.get("components", {}).items():
            if isinstance(entries, dict):
                merged["components"].setdefault(section, {}).update(entries)

        if "tags" in doc:
            merged.setdefault("tags", []).extend(doc["tags"])

        if "security" in doc:
            merged.setdefault("security", []).extend(doc["security"])

    return merged


def tag_paths_with_server(
    spec: dict[str, Any],
    paths: set[str],
    server: dict[str, Any],
) -> None:
    """Attach a specific server override to a set of paths.

    OpenAPI 3.1 allows per-path server overrides so clients know which
    base URL to use for each endpoint.
    """
    for path, path_item in spec["paths"].items():
        if path in paths:
            path_item["servers"] = [server]


def fill_streaming_endpoints(spec: dict[str, Any], streaming_rpcs: list[RpcMethod]) -> None:
    """Replace empty {} streaming path items with proper OpenAPI definitions.

    protoc-gen-connect-openapi emits {} for streaming RPCs because OpenAPI
    has no native streaming representation. We detect these from the proto
    files and fill them in with proper request/response schemas.
    """
    for rpc in streaming_rpcs:
        if rpc.path in spec["paths"]:
            print(f"    Filling streaming endpoint: {rpc.path} ({rpc.streaming_label})")
            spec["paths"][rpc.path] = build_streaming_path(rpc)


def apply_sandbox_auth(spec: dict[str, Any], envd_paths: set[str]) -> None:
    """Ensure all envd/sandbox endpoints declare the SandboxAccessTokenAuth security.

    The hand-written envd.yaml already has security declarations, but the
    proto-generated Connect RPC endpoints don't.  Add optional auth
    (SandboxAccessTokenAuth or anonymous) to any envd endpoint missing it.
    """
    auth_security = [{SANDBOX_AUTH_SCHEME: []}, {}]
    for path in envd_paths:
        path_item = spec["paths"].get(path)
        if not path_item:
            continue
        for method in ("get", "post", "put", "patch", "delete"):
            op = path_item.get(method)
            if op and "security" not in op:
                op["security"] = auth_security


def fix_security_schemes(spec: dict[str, Any]) -> None:
    """Fix invalid apiKey securityScheme syntax.

    The source envd.yaml uses `scheme: header` which is wrong for
    type: apiKey — OpenAPI requires `in: header` instead.
    """
    for scheme in spec.get("components", {}).get("securitySchemes", {}).values():
        if scheme.get("type") == "apiKey" and "scheme" in scheme:
            scheme["in"] = scheme.pop("scheme")


def _strip_supabase_security(path_item: dict[str, Any]) -> None:
    """Remove Supabase security entries from all operations in a path item.

    Each operation's security list is an OR of auth options. We remove
    any option that references a Supabase scheme, keeping the rest.
    """
    for method in ("get", "post", "put", "patch", "delete", "head", "options"):
        op = path_item.get(method)
        if not op or "security" not in op:
            continue
        op["security"] = [
            sec_req for sec_req in op["security"]
            if not any("supabase" in key.lower() for key in sec_req)
        ]


def _has_admin_token_security(path_item: dict[str, Any]) -> bool:
    """Check if any operation in a path item references AdminToken auth."""
    for method in ("get", "post", "put", "patch", "delete", "head", "options"):
        op = path_item.get(method)
        if not op:
            continue
        for sec_req in op.get("security", []):
            if any("admin" in key.lower() for key in sec_req):
                return True
    return False


def filter_paths(spec: dict[str, Any]) -> None:
    """Clean up paths that should not appear in the public spec.

    - Removes volume, access-token, and api-key endpoints
    - Removes endpoints using AdminToken auth
    - Strips Supabase auth entries from all operations
    - Removes Supabase and AdminToken securityScheme definitions
    """
    # Remove excluded paths
    excluded_prefixes = ("/volumes", "/access-tokens", "/api-keys")
    excluded_exact = {"/v2/sandboxes/{sandboxID}/logs", "/init"}
    to_remove = [
        p for p in spec["paths"]
        if p.startswith(excluded_prefixes) or p in excluded_exact
    ]

    # Remove admin-only paths
    for path, path_item in spec["paths"].items():
        if path not in to_remove and _has_admin_token_security(path_item):
            to_remove.append(path)

    for path in to_remove:
        del spec["paths"][path]
    if to_remove:
        print(f"==> Removed {len(to_remove)} paths (volumes + admin)")

    # Strip supabase security entries from all operations
    for path_item in spec["paths"].values():
        _strip_supabase_security(path_item)

    # Remove supabase and admin security scheme definitions
    schemes = spec.get("components", {}).get("securitySchemes", {})
    remove_keys = [k for k in schemes if "supabase" in k.lower() or "admin" in k.lower()]
    for key in remove_keys:
        del schemes[key]
    if remove_keys:
        print(f"==> Removed {len(remove_keys)} internal security schemes")


def remove_orphaned_schemas(spec: dict[str, Any]) -> None:
    """Remove component schemas that are not referenced anywhere in the spec.
    Runs iteratively since removing schemas may orphan others."""
    all_orphaned: list[str] = []

    while True:
        spec_text = ""
        # Serialize paths + top-level refs (excluding components.schemas itself)
        for section in ("paths", "security"):
            if section in spec:
                spec_text += yaml.dump(spec[section], default_flow_style=False)
        for section, entries in spec.get("components", {}).items():
            if section != "schemas":
                spec_text += yaml.dump(entries, default_flow_style=False)
        # Also check cross-references within schemas
        schemas = spec.get("components", {}).get("schemas", {})
        schema_text = yaml.dump(schemas, default_flow_style=False)

        orphaned = []
        for name in list(schemas.keys()):
            ref_pattern = f"schemas/{name}"
            # Referenced from paths/responses/params or from other schemas
            if ref_pattern not in spec_text and ref_pattern not in schema_text.replace(
                f"schemas/{name}:", ""  # exclude self-definition line
            ):
                # Double-check: not referenced by any other schema
                used = False
                for other_name, other_schema in schemas.items():
                    if other_name == name:
                        continue
                    if ref_pattern in yaml.dump(other_schema, default_flow_style=False):
                        used = True
                        break
                if not used:
                    orphaned.append(name)

        if not orphaned:
            break

        for name in orphaned:
            del schemas[name]
        all_orphaned.extend(orphaned)

    if all_orphaned:
        print(f"==> Removed {len(all_orphaned)} orphaned schemas: {', '.join(sorted(all_orphaned))}")


SANDBOX_NOT_FOUND_RESPONSE = {
    "description": "Sandbox not found",
    "content": {
        "application/json": {
            "schema": {
                "type": "object",
                "required": ["sandboxId", "message", "code"],
                "properties": {
                    "sandboxId": {
                        "type": "string",
                        "description": "Identifier of the sandbox",
                        "example": "i1234abcd5678efgh90jk",
                    },
                    "message": {
                        "type": "string",
                        "description": "Error message",
                        "example": "The sandbox was not found",
                    },
                    "code": {
                        "type": "integer",
                        "description": "Error code",
                        "example": 502,
                    },
                },
            }
        }
    },
}


EMPTY_RESPONSE_CONTENT = {
    "application/json": {
        "schema": {"type": "object", "description": "Empty response"}
    }
}


def add_sandbox_not_found(spec: dict[str, Any], envd_paths: set[str]) -> None:
    """Add a 502 response to all sandbox/envd endpoints.

    The load balancer returns 502 when a sandbox is not found.
    """
    count = 0
    for path in envd_paths:
        path_item = spec["paths"].get(path)
        if not path_item:
            continue
        for method in ("get", "post", "put", "patch", "delete"):
            op = path_item.get(method)
            if op and "502" not in op.get("responses", {}):
                op.setdefault("responses", {})["502"] = SANDBOX_NOT_FOUND_RESPONSE
                count += 1
    if count:
        print(f"==> Added 502 sandbox-not-found response to {count} operations")


def fill_empty_responses(spec: dict[str, Any]) -> None:
    """Add an empty content block to any 2xx response that lacks one.

    Mintlify requires a content block on every response to render correctly.
    """
    filled = 0
    stripped = 0
    for path, path_item in spec.get("paths", {}).items():
        for method in ("get", "post", "put", "patch", "delete", "head", "options"):
            op = path_item.get(method)
            if not op:
                continue
            responses = op.get("responses", {})
            # Remove "default" responses (generic Connect error envelopes)
            if "default" in responses:
                del responses["default"]
                stripped += 1
            for status, resp in responses.items():
                if isinstance(resp, dict) and str(status).startswith("2") and "content" not in resp:
                    resp["content"] = EMPTY_RESPONSE_CONTENT
                    filled += 1
    if filled:
        print(f"==> Added empty content block to {filled} responses")
    if stripped:
        print(f"==> Removed {stripped} default error responses")


# ---------------------------------------------------------------------------
# Entrypoint
# ---------------------------------------------------------------------------

def main() -> None:
    docker_build_image()

    # --- Sandbox API (envd) ---
    proto_docs = docker_generate_specs()
    envd_rest_doc = load_yaml_file(ENVD_REST_SPEC)

    # Track which paths come from envd so we can set their server
    envd_raw_docs = [envd_rest_doc] + proto_docs
    envd_paths: set[str] = set()
    for raw in envd_raw_docs:
        doc = yaml.safe_load(raw)
        if doc and "paths" in doc:
            envd_paths.update(doc["paths"].keys())

    # --- Platform API ---
    api_doc = load_yaml_file(API_SPEC)

    # --- Merge everything ---
    # Order: envd first, then platform API (platform schemas take precedence
    # for shared names like Error since they're more complete)
    merged = merge_specs(envd_raw_docs + [api_doc])

    # Auto-detect and fill streaming RPC endpoints
    streaming_rpcs = find_streaming_rpcs(ENVD_SPEC_DIR)
    print(f"==> Found {len(streaming_rpcs)} streaming RPCs in proto files")
    fill_streaming_endpoints(merged, streaming_rpcs)
    for rpc in streaming_rpcs:
        envd_paths.add(rpc.path)

    # Attach per-path server overrides so each path has exactly one server
    tag_paths_with_server(merged, envd_paths, SANDBOX_SERVER)
    platform_paths = set(merged["paths"].keys()) - envd_paths
    tag_paths_with_server(merged, platform_paths, PLATFORM_SERVER)

    # Ensure all sandbox endpoints declare auth
    apply_sandbox_auth(merged, envd_paths)

    # Add 502 sandbox-not-found to all envd endpoints
    add_sandbox_not_found(merged, envd_paths)

    # Fix known issues
    fix_security_schemes(merged)

    # Remove internal/unwanted paths
    filter_paths(merged)

    # Ensure all 2xx responses have a content block (required by Mintlify)
    fill_empty_responses(merged)

    # Clean up unreferenced schemas left over from filtered paths
    remove_orphaned_schemas(merged)

    # Write output
    output_path = os.path.join(os.getcwd(), "e2b-openapi.yml")
    with open(output_path, "w") as f:
        yaml.dump(merged, f, default_flow_style=False, sort_keys=False, allow_unicode=True)

    print(f"==> Written to {output_path}")


if __name__ == "__main__":
    main()
