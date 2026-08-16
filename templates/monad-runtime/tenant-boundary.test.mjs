import assert from 'node:assert/strict';
import test from 'node:test';

import {
  buildTenantBoundaryAttestation,
  TENANT_BOUNDARY_IDENTITY,
} from './tenant-boundary.mjs';

test('builds the closed immutable tenant-boundary claim for the pinned Webtop identity', () => {
  assert.deepEqual(TENANT_BOUNDARY_IDENTITY, {
    uid: 911,
    gid: 1001,
    groups: [100],
  });
  assert.deepEqual(buildTenantBoundaryAttestation({
    daemonSha256: 'a'.repeat(64),
    admissionHelperSha256: 'b'.repeat(64),
  }), {
    schema_version: 1,
    kind: 'monad.session-rebind-tenant-boundary',
    daemon: {
      executable_path: '/opt/monad/runtime/bin/monad-agent',
      sha256: 'a'.repeat(64),
      uid: 0,
    },
    tenant: {
      uid: 911,
      gid: 1001,
      groups: [100],
      services: {
        chromium: 911,
        git: 911,
        opencode: 911,
        selkies: 911,
        xorg: 911,
      },
    },
    cgroup: {
      version: 2,
      single_tenant_subtree: true,
      kill: true,
      detached_descendant_probe: true,
      root_daemon_survives_kill: true,
    },
    admission_helper: {
      path: '/usr/local/libexec/monad-tenant-admission',
      sha256: 'b'.repeat(64),
      protocol_version: 3,
    },
    runtime_config_digest: {
      algorithm: 'monad.runtime-config.sha256.v1',
    },
  });
});

test('rejects ambiguous or noncanonical prepared artifact hashes', () => {
  for (const value of [
    '',
    'A'.repeat(64),
    'a'.repeat(63),
    'a'.repeat(65),
    `${'a'.repeat(63)}g`,
  ]) {
    assert.throws(() => buildTenantBoundaryAttestation({
      daemonSha256: value,
      admissionHelperSha256: 'b'.repeat(64),
    }));
    assert.throws(() => buildTenantBoundaryAttestation({
      daemonSha256: 'a'.repeat(64),
      admissionHelperSha256: value,
    }));
  }
});
