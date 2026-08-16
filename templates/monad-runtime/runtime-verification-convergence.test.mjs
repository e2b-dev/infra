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

test('requires an exact root identity without returning raw identity data', () => {
  assert.deepEqual(
    convergence.classifyTenantBoundaryProbeIdentity({
      getuid: () => 0,
      getgid: () => 0,
    }),
    { probe_ok: true },
  );
  for (const identity of [
    { getuid: () => 911, getgid: () => 0 },
    { getuid: () => 0, getgid: () => 1001 },
    { getuid: () => { throw new Error('private uid failure'); }, getgid: () => 0 },
    { getuid: () => 0, getgid: () => { throw new Error('private gid failure'); } },
  ]) {
    assert.deepEqual(
      convergence.classifyTenantBoundaryProbeIdentity(identity),
      { probe_ok: false, stage: 'probe_identity' },
    );
  }
});

test('isolates marker filesystem failures into exact safe stages', () => {
  const paths = {
    procRootRun: '/proc/1006/root/run',
    admissionRoot: '/proc/1006/root/run/monad-admission',
    marker: '/proc/1006/root/run/monad-admission/tenant-cgroup-ready',
  };
  const directory = (overrides = {}) => ({
    isDirectory: () => true,
    isFile: () => false,
    isSymbolicLink: () => false,
    uid: 0,
    gid: 0,
    mode: 0o700,
    ...overrides,
  });
  const file = (overrides = {}) => ({
    isDirectory: () => false,
    isFile: () => true,
    isSymbolicLink: () => false,
    uid: 0,
    gid: 0,
    mode: 0o444,
    ...overrides,
  });
  const valid = new Map([
    [paths.procRootRun, directory()],
    [paths.admissionRoot, directory()],
    [paths.marker, file()],
  ]);
  const inspect = ({ path, value, throws = false } = {}) =>
    convergence.inspectTenantBoundaryMarkerFilesystem({
      ...paths,
      lstat: (candidate) => {
        if (candidate === path && throws) throw new Error('private lstat failure');
        return candidate === path ? value : valid.get(candidate);
      },
    });

  assert.deepEqual(inspect(), { probe_ok: true });
  for (const testCase of [
    [paths.procRootRun, undefined, true, 'marker_proc_root_run_access'],
    [paths.procRootRun, directory({ isDirectory: () => false }), false,
      'marker_proc_root_run_type'],
    [paths.procRootRun, directory({ isSymbolicLink: () => true }), false,
      'marker_proc_root_run_type'],
    [paths.procRootRun, directory({ uid: 911 }), false,
      'marker_proc_root_run_owner'],
    [paths.admissionRoot, undefined, true, 'marker_directory_access'],
    [paths.admissionRoot, directory({ isDirectory: () => false }), false,
      'marker_directory_type'],
    [paths.admissionRoot, directory({ isSymbolicLink: () => true }), false,
      'marker_directory_type'],
    [paths.admissionRoot, directory({ gid: 1001 }), false,
      'marker_directory_owner'],
    [paths.admissionRoot, directory({ mode: 0o755 }), false,
      'marker_directory_mode'],
    [paths.marker, undefined, true, 'marker_file_access'],
    [paths.marker, file({ isFile: () => false }), false, 'marker_file_type'],
    [paths.marker, file({ isSymbolicLink: () => true }), false,
      'marker_file_type'],
    [paths.marker, file({ uid: 911 }), false, 'marker_file_owner'],
    [paths.marker, file({ mode: 0o400 }), false, 'marker_file_mode'],
  ]) {
    const [path, value, throws, stage] = testCase;
    assert.deepEqual(inspect({ path, value, throws }), {
      probe_ok: false,
      stage,
    });
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
    'marker_proc_root_run_access',
    'marker_proc_root_run_type',
    'marker_proc_root_run_owner',
    'marker_directory_access',
    'marker_directory_type',
    'marker_directory_owner',
    'marker_directory_mode',
    'marker_file_access',
    'marker_file_type',
    'marker_file_owner',
    'marker_file_mode',
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

test('fails closed immediately when the verifier is not root', async () => {
  let calls = 0;
  await assert.rejects(waitForTenantBoundaryEvidence({
    probe: async () => {
      calls += 1;
      return { probe_ok: false, stage: 'probe_identity' };
    },
    now: () => 0,
    sleep: async () => assert.fail('terminal identity failure must not retry'),
    timeoutMs: 1_000,
    intervalMs: 100,
  }), (error) => {
    assert.ok(error instanceof TenantBoundaryConvergenceError);
    assert.equal(error.stage, 'probe_identity');
    assert.equal(error.message, 'tenant boundary did not converge at probe_identity');
    return true;
  });
  assert.equal(calls, 1);
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
    { probe_ok: false, stage: 'probe_identity', uid: 911 },
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
