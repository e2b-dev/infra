import assert from 'node:assert/strict';
import test from 'node:test';

import * as convergence from './runtime-verification-convergence.mjs';

import {
  classifyTenantBoundaryEvidence,
  tenantBoundaryProcRootPaths,
  tenantBoundaryRuntimePath,
  tenantBoundaryRuntimePathChain,
  TenantBoundaryConvergenceError,
  waitForTenantBoundaryEvidence,
} from './runtime-verification-convergence.mjs';

test('binds the marker to the daemon current cgroup and derives tenant membership', () => {
  assert.equal(typeof convergence.bindTenantBoundaryMarker, 'function');
  assert.deepEqual(
    convergence.bindTenantBoundaryMarker(
      '0::/user',
      '/sys/fs/cgroup/user/monad-tenant\n',
    ),
    {
      tenantCgroup: '/sys/fs/cgroup/user/monad-tenant',
      expectedMembership: '0::/user/monad-tenant',
    },
  );
  assert.deepEqual(
    convergence.bindTenantBoundaryMarker(
      '0::/',
      '/sys/fs/cgroup/monad-tenant\n',
    ),
    {
      tenantCgroup: '/sys/fs/cgroup/monad-tenant',
      expectedMembership: '0::/monad-tenant',
    },
  );
});

test('rejects a marker outside the exact daemon cgroup child binding', () => {
  const invalid = [
    ['0::/user', '/sys/fs/cgroup/monad-tenant\n'],
    ['0::/user', '/sys/fs/cgroup/sibling/monad-tenant\n'],
    ['0::/user', '/sys/fs/cgroup/user/../monad-tenant\n'],
    ['0::/user', '/sys/fs/cgroup/user/monad-tenant\nextra\n'],
    ['0::/user', '/sys/fs/cgroup/user/monad-tenant\0\n'],
    ['0::', '/sys/fs/cgroup/monad-tenant\n'],
    ['1::/user', '/sys/fs/cgroup/user/monad-tenant\n'],
    ['0:://user', '/sys/fs/cgroup/user/monad-tenant\n'],
    ['0::/user/../other', '/sys/fs/cgroup/user/monad-tenant\n'],
    ['0::/user\n0::/other', '/sys/fs/cgroup/user/monad-tenant\n'],
  ];
  for (const [daemonMembership, marker] of invalid) {
    assert.throws(
      () => convergence.bindTenantBoundaryMarker(daemonMembership, marker),
      /tenant boundary marker binding is invalid/,
    );
  }
});

test('binds marker inspection to one validated supervisor proc root', () => {
  assert.deepEqual(tenantBoundaryProcRootPaths(1006), {
    root: '/proc/1006/root',
    admissionRoot: '/proc/1006/root/run/monad-admission',
    marker: '/proc/1006/root/run/monad-admission/tenant-cgroup-ready',
  });
  for (const invalid of [undefined, null, 0, 1, -1, 1.5, NaN, Infinity, '1006']) {
    assert.throws(
      () => tenantBoundaryProcRootPaths(invalid),
      /tenant boundary supervisor PID is invalid/,
    );
  }
});

test('enumerates every immutable runtime ancestor below the proc-root anchor', () => {
  assert.deepEqual(
    tenantBoundaryRuntimePathChain(1006, '/etc/monad/runtime.json'),
    [
      '/proc/1006/root/etc',
      '/proc/1006/root/etc/monad',
      '/proc/1006/root/etc/monad/runtime.json',
    ],
  );
});

test('maps immutable runtime evidence through the same supervisor proc root', () => {
  assert.equal(
    tenantBoundaryRuntimePath(1006, '/etc/monad/session-rebind-tenant-boundary.json'),
    '/proc/1006/root/etc/monad/session-rebind-tenant-boundary.json',
  );
  for (const invalid of [
    'etc/monad/file',
    '//etc/monad/file',
    '/etc/../shadow',
    '/etc//shadow',
    '/etc/./shadow',
    '/etc/monad\0file',
  ]) {
    assert.throws(
      () => tenantBoundaryRuntimePath(1006, invalid),
      /tenant boundary runtime path is invalid/,
    );
  }
});

const readyEvidence = Object.freeze({
  root_daemon_outside_tenant_cgroup: true,
  tenant_service_identity_match: true,
  nginx_identity_match: true,
  watchdog_identity_match: true,
  tenant_service_cgroup_match: true,
  service_leader_cgroup_match: true,
  important_descendant_cgroup_match: true,
  service_state_stable: true,
  supervisor_state_stable: true,
  daemon_executable_match: true,
  attestation_hash_match: true,
  attestation_identity_match: true,
  attestation_files_exact: true,
  marker_exact: true,
  marker_basename_match: true,
  marker_parent_daemon_cgroup_match: true,
});

test('classifies startup-dependent process and filesystem evidence before success', () => {
  assert.deepEqual(classifyTenantBoundaryEvidence({
    ...readyEvidence,
    important_descendant_cgroup_match: false,
  }), { probe_ok: false, stage: 'process_attestation' });
  assert.deepEqual(classifyTenantBoundaryEvidence({
    ...readyEvidence,
    attestation_files_exact: false,
  }), { probe_ok: false, stage: 'filesystem_attestation' });
  const complete = { ...readyEvidence, service_leaders: { nginx: 42 } };
  assert.deepEqual(classifyTenantBoundaryEvidence(complete), {
    probe_ok: true,
    evidence: complete,
  });
});

test('waits for a bounded tenant-boundary startup stage and returns exact evidence', async () => {
  const records = [
    { probe_ok: false, stage: 'service_mapping' },
    { probe_ok: true, evidence: { marker_exact: true } },
  ];
  let now = 1_000;
  const sleeps = [];
  const remainingBudgets = [];
  const evidence = await waitForTenantBoundaryEvidence({
    probe: async ({ remainingMs }) => {
      remainingBudgets.push(remainingMs);
      return records.shift();
    },
    now: () => now,
    sleep: async (milliseconds) => {
      sleeps.push(milliseconds);
      now += milliseconds;
    },
    timeoutMs: 10_000,
    intervalMs: 2_000,
  });
  assert.deepEqual(evidence, { marker_exact: true });
  assert.deepEqual(sleeps, [2_000]);
  assert.deepEqual(remainingBudgets, [10_000, 8_000]);
});

test('accepts only bounded safe marker convergence stages', async () => {
  for (const stage of [
    'marker_directory',
    'marker_file',
    'marker_binding',
    'marker_target',
  ]) {
    let now = 0;
    const records = [
      { probe_ok: false, stage },
      { probe_ok: true, evidence: { marker_exact: true } },
    ];
    await assert.doesNotReject(waitForTenantBoundaryEvidence({
      probe: async () => records.shift(),
      now: () => now,
      sleep: async (milliseconds) => { now += milliseconds; },
      timeoutMs: 1_000,
      intervalMs: 100,
    }));
  }
});

test('fails with only a safe stage when tenant-boundary convergence expires', async () => {
  let now = 5_000;
  let calls = 0;
  await assert.rejects(
    waitForTenantBoundaryEvidence({
      probe: async () => {
        calls += 1;
        return { probe_ok: false, stage: 'process_attestation' };
      },
      now: () => now,
      sleep: async (milliseconds) => { now += milliseconds; },
      timeoutMs: 3_000,
      intervalMs: 2_000,
    }),
    (error) => {
      assert.ok(error instanceof TenantBoundaryConvergenceError);
      assert.equal(error.stage, 'process_attestation');
      assert.equal(error.message, 'tenant boundary did not converge at process_attestation');
      return true;
    },
  );
  assert.equal(now, 8_000);
  assert.equal(calls, 2, 'the deadline must be checked before another probe');
});

test('rejects malformed or expanded probe records without retrying', async () => {
  for (const record of [
    null,
    { probe_ok: false, stage: 'unknown' },
    { probe_ok: false, stage: 'service_mapping', diagnostic: 'secret' },
    { probe_ok: true, evidence: null },
    { probe_ok: true, evidence: {}, extra: true },
  ]) {
    let calls = 0;
    await assert.rejects(waitForTenantBoundaryEvidence({
      probe: async () => { calls += 1; return record; },
      now: () => 0,
      sleep: async () => assert.fail('malformed evidence must not retry'),
      timeoutMs: 1_000,
      intervalMs: 100,
    }), /tenant boundary probe returned an invalid record/);
    assert.equal(calls, 1);
  }
});

test('rejects success that arrives only after the shared wall-clock deadline', async () => {
  let now = 10_000;
  await assert.rejects(waitForTenantBoundaryEvidence({
    probe: async ({ remainingMs }) => {
      now += remainingMs;
      return { probe_ok: true, evidence: { marker_exact: true } };
    },
    now: () => now,
    sleep: async () => assert.fail('late success must not sleep'),
    timeoutMs: 1_000,
    intervalMs: 100,
  }), (error) => {
    assert.ok(error instanceof TenantBoundaryConvergenceError);
    assert.equal(error.stage, 'marker');
    return true;
  });
});
