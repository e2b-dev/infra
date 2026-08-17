import assert from 'node:assert/strict';
import test from 'node:test';

import * as convergence from './runtime-verification-convergence.mjs';

import {
  classifyTenantBoundaryFilesystemStability,
  classifyTenantBoundaryEvidence,
  classifyTenantBoundaryTopology,
  TENANT_BOUNDARY_CONVERGENCE_TIMEOUT_MS,
  tenantBoundaryProcRootPaths,
  tenantBoundaryProbeTimeoutMs,
  tenantBoundaryRuntimePath,
  tenantBoundaryRuntimePathChain,
  TenantBoundaryConvergenceError,
  waitForTenantBoundaryEvidence,
} from './runtime-verification-convergence.mjs';

test('reserves bounded transport headroom inside the tenant verification deadline', () => {
  assert.equal(TENANT_BOUNDARY_CONVERGENCE_TIMEOUT_MS, 175_000);
  for (const [remainingMs, elapsedMs, expectedTimeoutMs] of [
    [175_000, 0, 35_000],
    [30_000, 145_000, 35_000],
    [1_600, 173_400, 6_600],
    [1, 174_999, 5_001],
  ]) {
    const timeoutMs = tenantBoundaryProbeTimeoutMs(remainingMs);
    assert.equal(timeoutMs, expectedTimeoutMs);
    assert.ok(elapsedMs + timeoutMs <= 180_000);
  }

  for (const remainingMs of [
    undefined,
    null,
    0,
    -1,
    1.5,
    Number.NaN,
    Number.POSITIVE_INFINITY,
    175_001,
  ]) {
    assert.throws(
      () => tenantBoundaryProbeTimeoutMs(remainingMs),
      /tenant boundary probe remaining time is invalid/,
    );
  }
});

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
      authority: 'daemon',
      lstat: (candidate) => {
        if (candidate === path && throws) throw new Error('private lstat failure');
        return candidate === path ? value : valid.get(candidate);
      },
    });

  assert.deepEqual(inspect(), { probe_ok: true });
  for (const testCase of [
    [paths.procRootRun, undefined, true, 'marker_daemon_proc_root_run_error'],
    [paths.procRootRun, directory({ isDirectory: () => false }), false,
      'marker_daemon_proc_root_run_type'],
    [paths.procRootRun, directory({ isSymbolicLink: () => true }), false,
      'marker_daemon_proc_root_run_type'],
    [paths.procRootRun, directory({ uid: 911 }), false,
      'marker_daemon_proc_root_run_owner'],
    [paths.admissionRoot, undefined, true, 'marker_daemon_directory_error'],
    [paths.admissionRoot, directory({ isDirectory: () => false }), false,
      'marker_daemon_directory_type'],
    [paths.admissionRoot, directory({ isSymbolicLink: () => true }), false,
      'marker_daemon_directory_type'],
    [paths.admissionRoot, directory({ gid: 1001 }), false,
      'marker_daemon_directory_owner'],
    [paths.admissionRoot, directory({ mode: 0o755 }), false,
      'marker_daemon_directory_mode'],
    [paths.marker, undefined, true, 'marker_daemon_file_error'],
    [paths.marker, file({ isFile: () => false }), false,
      'marker_daemon_file_type'],
    [paths.marker, file({ isSymbolicLink: () => true }), false,
      'marker_daemon_file_type'],
    [paths.marker, file({ uid: 911 }), false, 'marker_daemon_file_owner'],
    [paths.marker, file({ mode: 0o400 }), false, 'marker_daemon_file_mode'],
  ]) {
    const [path, value, throws, stage] = testCase;
    assert.deepEqual(inspect({ path, value, throws }), {
      probe_ok: false,
      stage,
    });
  }
});

test('classifies marker lstat failures without leaking raw error details', () => {
  const paths = {
    procRootRun: '/proc/1006/root/run',
    admissionRoot: '/proc/1006/root/run/monad-admission',
    marker: '/proc/1006/root/run/monad-admission/tenant-cgroup-ready',
  };
  const cases = [
    [{ code: 'ENOENT', message: 'private missing path' }, 'missing'],
    [{ code: 'EACCES', message: 'private denied path' }, 'denied'],
    [{ code: 'EPERM', message: 'private denied operation' }, 'denied'],
    [{ code: 'EIO', message: 'private device detail' }, 'error'],
    ['private non-object failure', 'error'],
  ];
  for (const authority of ['daemon', 'supervisor']) {
    for (const [thrown, category] of cases) {
      const result = convergence.inspectTenantBoundaryMarkerFilesystem({
        ...paths,
        authority,
        lstat: () => { throw thrown; },
      });
      assert.deepEqual(result, {
        probe_ok: false,
        stage: `marker_${authority}_proc_root_run_${category}`,
      });
      const serialized = JSON.stringify(result);
      assert.doesNotMatch(serialized, /private|ENOENT|EACCES|EPERM|EIO/);
    }
  }
});

test('requires complete marker evidence through daemon and supervisor roots', () => {
  const metadata = (type, mode) => ({
    isDirectory: () => type === 'directory',
    isFile: () => type === 'file',
    isSymbolicLink: () => false,
    uid: 0,
    gid: 0,
    mode,
  });
  const inspect = (authority, missingDirectory = false) =>
    convergence.inspectTenantBoundaryMarkerFilesystem({
      authority,
      procRootRun: `/${authority}/run`,
      admissionRoot: `/${authority}/run/monad-admission`,
      marker: `/${authority}/run/monad-admission/tenant-cgroup-ready`,
      lstat: (path) => {
        if (missingDirectory && path.endsWith('/monad-admission')) {
          throw Object.assign(new Error('raw missing path'), { code: 'ENOENT' });
        }
        return path.endsWith('tenant-cgroup-ready')
          ? metadata('file', 0o444)
          : metadata('directory', 0o700);
      },
    });
  assert.deepEqual(inspect('daemon'), { probe_ok: true });
  assert.deepEqual(inspect('supervisor', true), {
    probe_ok: false,
    stage: 'marker_supervisor_directory_missing',
  });
});

test('binds the daemon service mapping and rejects split runtime topology safely', () => {
  const complete = {
    daemonPid: 284,
    supervisorPid: 1006,
    daemonServicePid: 284,
    daemonNamespacePids: [284],
    supervisorNamespacePids: [1006],
    daemonNamespace: 'pid:[4026533000]',
    supervisorNamespace: 'pid:[4026533000]',
    daemonMountNamespace: 'mnt:[4026533001]',
    supervisorMountNamespace: 'mnt:[4026533001]',
    daemonRoot: { dev: 1, ino: 2 },
    supervisorRoot: { dev: 1, ino: 2 },
    daemonRun: { dev: 3, ino: 4 },
    supervisorRun: { dev: 3, ino: 4 },
  };
  assert.deepEqual(classifyTenantBoundaryTopology(complete), { probe_ok: true });
  for (const [overrides, stage] of [
    [{ daemonServicePid: 999 }, 'daemon_service_mapping'],
    [{ daemonNamespacePids: [284, 7] }, 'daemon_service_mapping'],
    [{ daemonNamespace: 'pid:[other]' }, 'daemon_service_mapping'],
    [{ daemonMountNamespace: 'mnt:[other]' },
      'daemon_supervisor_mount_namespace'],
    [{ daemonRoot: { dev: 1, ino: 9 } },
      'daemon_supervisor_root_identity'],
    [{ daemonRun: { dev: 3, ino: 9 } },
      'daemon_supervisor_run_identity'],
  ]) {
    assert.deepEqual(classifyTenantBoundaryTopology({
      ...complete,
      ...overrides,
    }), { probe_ok: false, stage });
  }
});

test('rejects followed root or run replacement after namespace topology is pinned', () => {
  const stable = {
    initialDaemonRoot: { dev: 1, ino: 2 },
    initialSupervisorRoot: { dev: 1, ino: 2 },
    initialDaemonRun: { dev: 3, ino: 4 },
    initialSupervisorRun: { dev: 3, ino: 4 },
    finalDaemonRoot: { dev: 1, ino: 2 },
    finalSupervisorRoot: { dev: 1, ino: 2 },
    finalDaemonRun: { dev: 3, ino: 4 },
    finalSupervisorRun: { dev: 3, ino: 4 },
  };
  assert.deepEqual(classifyTenantBoundaryFilesystemStability(stable), {
    probe_ok: true,
  });
  const splitInitialTopology = classifyTenantBoundaryFilesystemStability({
    ...stable,
    initialSupervisorRoot: { dev: 1, ino: 9 },
  });
  assert.deepEqual(splitInitialTopology, {
    probe_ok: false,
    stage: 'daemon_supervisor_filesystem_stability',
  });
  assert.doesNotMatch(JSON.stringify(splitInitialTopology), /dev|ino|[1-9]/);
  for (const [overrides, stage] of [
    [{ finalDaemonRoot: { dev: 1, ino: 9 } }, 'daemon_root_identity_stable'],
    [{ finalSupervisorRoot: { dev: 9, ino: 2 } }, 'supervisor_root_identity_stable'],
    [{ finalDaemonRun: { dev: 3, ino: 9 } }, 'daemon_run_identity_stable'],
    [{ finalSupervisorRun: { dev: 9, ino: 4 } }, 'supervisor_run_identity_stable'],
  ]) {
    const result = classifyTenantBoundaryFilesystemStability({
      ...stable,
      ...overrides,
    });
    assert.deepEqual(result, {
      probe_ok: false,
      stage,
    });
    assert.doesNotMatch(JSON.stringify(result), /dev|ino|[1-9]/);
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
  daemon_service_mapping: true,
  daemon_supervisor_mount_namespace_match: true,
  daemon_supervisor_root_identity_match: true,
  daemon_supervisor_run_identity_match: true,
  daemon_supervisor_filesystem_stable: true,
  service_leader_mount_namespace_match: true,
  daemon_state_stable: true,
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
  const processStages = [
    'daemon_supervisor_mount_namespace_match',
    'daemon_supervisor_root_identity_match',
    'daemon_supervisor_run_identity_match',
    'daemon_supervisor_filesystem_stable',
    'service_leader_mount_namespace_match',
    'daemon_state_stable',
    'root_daemon_outside_tenant_cgroup',
    'tenant_service_identity_match',
    'nginx_identity_match',
    'watchdog_identity_match',
    'tenant_service_cgroup_match',
    'service_leader_cgroup_match',
    'important_descendant_cgroup_match',
    'service_state_stable',
    'supervisor_state_stable',
  ];
  for (const stage of processStages) {
    assert.deepEqual(classifyTenantBoundaryEvidence({
      ...readyEvidence,
      [stage]: false,
    }), { probe_ok: false, stage });
  }
  assert.deepEqual(classifyTenantBoundaryEvidence({
    ...readyEvidence,
    daemon_service_mapping: false,
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

test('accepts every fixed process-attestation substage as bounded retryable evidence', async () => {
  for (const stage of [
    'daemon_supervisor_mount_namespace_match',
    'daemon_supervisor_root_identity_match',
    'daemon_supervisor_run_identity_match',
    'daemon_supervisor_filesystem_stable',
    'service_leader_mount_namespace_match',
    'daemon_state_stable',
    'root_daemon_outside_tenant_cgroup',
    'tenant_service_identity_match',
    'nginx_identity_match',
    'watchdog_identity_match',
    'tenant_service_cgroup_match',
    'service_leader_cgroup_match',
    'important_descendant_cgroup_match',
    'service_state_stable',
    'supervisor_state_stable',
  ]) {
    let now = 0;
    let sleeps = 0;
    await assert.rejects(waitForTenantBoundaryEvidence({
      probe: async () => ({ probe_ok: false, stage }),
      now: () => now,
      sleep: async (milliseconds) => {
        sleeps += 1;
        now += milliseconds;
      },
      timeoutMs: 100,
      intervalMs: 100,
    }), (error) => {
      assert.ok(error instanceof TenantBoundaryConvergenceError);
      assert.equal(error.stage, stage);
      return true;
    });
    assert.equal(sleeps, 1, `${stage} must remain retryable until the deadline`);
  }
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
    'marker_daemon_proc_root_run_missing',
    'marker_daemon_directory_missing',
    'marker_daemon_file_missing',
    'marker_supervisor_proc_root_run_missing',
    'marker_supervisor_directory_missing',
    'marker_supervisor_file_missing',
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

test('accepts every fixed final filesystem access and identity stage as retryable evidence', async () => {
  for (const stage of [
    'daemon_root_final_access',
    'supervisor_root_final_access',
    'daemon_run_final_access',
    'supervisor_run_final_access',
    'daemon_root_identity_stable',
    'supervisor_root_identity_stable',
    'daemon_run_identity_stable',
    'supervisor_run_identity_stable',
  ]) {
    let now = 0;
    let calls = 0;
    const sleeps = [];
    const evidence = await waitForTenantBoundaryEvidence({
      probe: async () => {
        calls += 1;
        if (calls === 1) return { probe_ok: false, stage };
        return { probe_ok: true, evidence: { filesystem_stable: true } };
      },
      now: () => now,
      sleep: async (milliseconds) => {
        sleeps.push(milliseconds);
        now += milliseconds;
      },
      timeoutMs: 1_000,
      intervalMs: 100,
    });
    assert.deepEqual(evidence, { filesystem_stable: true });
    assert.equal(calls, 2);
    assert.deepEqual(sleeps, [100]);
  }
});

test('accepts only fixed per-service important-descendant stages as retryable evidence', async () => {
  for (const service of [
    'nginx', 'xorg', 'dbus', 'pulseaudio', 'selkies', 'de', 'watchdog',
    'xsettingsd',
  ]) {
    const missingCategories = service === 'watchdog'
      ? [
          'missing_multi',
          'missing_executable_helper_ready_absent',
          'missing_executable_helper_ready_present',
          'missing_executable_helper_ready_error',
          'missing_executable_shell',
          'missing_executable_package_tree',
          'missing_executable_command_tree',
          'missing_executable_usr_bin',
          'missing_executable_elsewhere',
          'missing_argv',
        ]
      : ['missing_leader_only', 'missing_no_match'];
    for (const category of [...missingCategories, 'access', 'cgroup']) {
      const stage = `important_descendant_${service}_${category}`;
      let now = 0;
      let calls = 0;
      const evidence = await waitForTenantBoundaryEvidence({
        probe: async () => {
          calls += 1;
          if (calls === 1) return { probe_ok: false, stage };
          return { probe_ok: true, evidence: { descendants_exact: true } };
        },
        now: () => now,
        sleep: async (milliseconds) => { now += milliseconds; },
        timeoutMs: 1_000,
        intervalMs: 100,
      });
      assert.deepEqual(evidence, { descendants_exact: true });
      assert.equal(calls, 2);
      assert.equal(now, 100);
    }
  }
  await assert.rejects(waitForTenantBoundaryEvidence({
    probe: async () => ({
      probe_ok: false,
      stage: 'important_descendant_unknown_access',
    }),
    now: () => 0,
    sleep: async () => assert.fail('unknown descendant stage must not retry'),
    timeoutMs: 1_000,
    intervalMs: 100,
  }), /invalid record/);
  for (const retiredStage of [
    'important_descendant_watchdog_missing',
    'important_descendant_watchdog_missing_executable',
    'important_descendant_watchdog_missing_executable_helper',
    'important_descendant_watchdog_missing_executable_other',
    'important_descendant_nginx_missing',
  ]) {
    await assert.rejects(waitForTenantBoundaryEvidence({
      probe: async () => ({ probe_ok: false, stage: retiredStage }),
      now: () => 0,
      sleep: async () => assert.fail('retired unsplit missing stage must not retry'),
      timeoutMs: 1_000,
      intervalMs: 100,
    }), /invalid record/);
  }
});

test('retries a discarded cross-time filesystem sample and accepts only a later stable probe', async () => {
  let now = 0;
  let calls = 0;
  const sleeps = [];
  const evidence = await waitForTenantBoundaryEvidence({
    probe: async () => {
      calls += 1;
      if (calls === 1) {
        return {
          probe_ok: false,
          stage: 'daemon_root_identity_stable',
        };
      }
      return { probe_ok: true, evidence: { filesystem_stable: true } };
    },
    now: () => now,
    sleep: async (milliseconds) => {
      sleeps.push(milliseconds);
      now += milliseconds;
    },
    timeoutMs: 1_000,
    intervalMs: 100,
  });
  assert.deepEqual(evidence, { filesystem_stable: true });
  assert.equal(calls, 2);
  assert.deepEqual(sleeps, [100]);
});

test('persistent cross-time filesystem instability remains fail-closed at the deadline', async () => {
  let now = 0;
  let calls = 0;
  await assert.rejects(waitForTenantBoundaryEvidence({
    probe: async () => {
      calls += 1;
      return {
        probe_ok: false,
        stage: 'supervisor_run_identity_stable',
      };
    },
    now: () => now,
    sleep: async (milliseconds) => { now += milliseconds; },
    timeoutMs: 300,
    intervalMs: 100,
  }), (error) => {
    assert.ok(error instanceof TenantBoundaryConvergenceError);
    assert.equal(error.stage, 'supervisor_run_identity_stable');
    assert.equal(
      error.message,
      'tenant boundary did not converge at supervisor_run_identity_stable',
    );
    return true;
  });
  assert.equal(calls, 3);
  assert.equal(now, 300);
});

test('fails closed immediately for denied, unexpected, and topology stages', async () => {
  for (const stage of [
    'marker_daemon_directory_denied',
    'marker_daemon_directory_error',
    'marker_supervisor_directory_denied',
    'marker_supervisor_directory_error',
    'marker_daemon_directory_type',
    'marker_supervisor_file_mode',
    'marker_binding',
    'marker_target',
    'daemon_service_mapping',
    'daemon_supervisor_mount_namespace',
    'daemon_supervisor_root_identity',
    'daemon_supervisor_run_identity',
    'daemon_supervisor_filesystem_stability',
  ]) {
    let calls = 0;
    await assert.rejects(waitForTenantBoundaryEvidence({
      probe: async () => { calls += 1; return { probe_ok: false, stage }; },
      now: () => 0,
      sleep: async () => assert.fail('terminal topology evidence must not retry'),
      timeoutMs: 1_000,
      intervalMs: 100,
    }), (error) => {
      assert.ok(error instanceof TenantBoundaryConvergenceError);
      assert.equal(error.stage, stage);
      return true;
    });
    assert.equal(calls, 1);
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
  let calls = 0;
  await assert.rejects(waitForTenantBoundaryEvidence({
    probe: async ({ remainingMs }) => {
      calls += 1;
      if (calls === 1) {
        return { probe_ok: false, stage: 'process_attestation' };
      }
      now += remainingMs;
      return { probe_ok: true, evidence: { marker_exact: true } };
    },
    now: () => now,
    sleep: async (milliseconds) => { now += milliseconds; },
    timeoutMs: 1_000,
    intervalMs: 100,
  }), (error) => {
    assert.ok(error instanceof TenantBoundaryConvergenceError);
    assert.equal(error.stage, 'process_attestation');
    return true;
  });
  assert.equal(calls, 2);
});

test('preserves the last safe stage when probe transport rejects after the logical deadline', async () => {
  let now = 20_000;
  let calls = 0;
  await assert.rejects(waitForTenantBoundaryEvidence({
    probe: async ({ remainingMs }) => {
      calls += 1;
      if (calls === 1) {
        return { probe_ok: false, stage: 'process_attestation' };
      }
      now += remainingMs;
      throw new Error('synthetic transport deadline');
    },
    now: () => now,
    sleep: async (milliseconds) => { now += milliseconds; },
    timeoutMs: 1_000,
    intervalMs: 100,
  }), (error) => {
    assert.ok(error instanceof TenantBoundaryConvergenceError);
    assert.equal(error.stage, 'process_attestation');
    return true;
  });
  assert.equal(calls, 2);
});

test('does not mask a probe transport rejection before the logical deadline', async () => {
  const transportError = new Error('synthetic early transport failure');
  await assert.rejects(waitForTenantBoundaryEvidence({
    probe: async () => { throw transportError; },
    now: () => 30_000,
    sleep: async () => assert.fail('transport rejection must not retry'),
    timeoutMs: 1_000,
    intervalMs: 100,
  }), (error) => error === transportError);
});
