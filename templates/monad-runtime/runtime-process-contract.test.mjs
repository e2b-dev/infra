import assert from 'node:assert/strict';
import test from 'node:test';

import {
  parseNamespacePidVector,
  selectOuterPidForNamespacePid,
  verifyPinnedNginxProcesses,
  verifyPinnedWatchdogProcesses,
} from './runtime-process-contract.mjs';

test('selects exactly one outer process for a nested or same-namespace service PID', () => {
  assert.deepEqual(
    parseNamespacePidVector('Name:\tnginx\nNSpid:\t4100\t278\n'),
    [4100, 278],
  );
  assert.equal(selectOuterPidForNamespacePid({
    innerPid: 278,
    expectedMembership: '0::/monad-tenant',
    expectedNamespace: 'pid:[4026533000]',
    expectedNamespaceDepth: 2,
    processes: [
      {
        pid: 4100,
        namespacePids: [4100, 278],
        namespace: 'pid:[4026533000]',
        cgroup: '0::/monad-tenant',
      },
      {
        pid: 5100,
        namespacePids: [5100, 278],
        namespace: 'pid:[4026534000]',
        cgroup: '0::/unrelated',
      },
    ],
  }), 4100);
  assert.equal(selectOuterPidForNamespacePid({
    innerPid: 278,
    expectedMembership: '0::/monad-tenant',
    expectedNamespace: 'pid:[4026531000]',
    expectedNamespaceDepth: 1,
    processes: [
      {
        pid: 278,
        namespacePids: [278],
        namespace: 'pid:[4026531000]',
        cgroup: '0::/monad-tenant',
      },
    ],
  }), 278);
});

test('rejects malformed, absent, or ambiguous namespace PID evidence', () => {
  for (const raw of [
    '',
    'Name:\tnginx\n',
    'NSpid:\t0\n',
    'NSpid:\t4100 nope\n',
    'NSpid:\t4100\nNSpid:\t4100\n',
  ]) {
    assert.deepEqual(parseNamespacePidVector(raw), []);
  }
  const base = {
    innerPid: 278,
    expectedMembership: '0::/monad-tenant',
    expectedNamespace: 'pid:[4026533000]',
    expectedNamespaceDepth: 2,
  };
  assert.throws(() => selectOuterPidForNamespacePid({ ...base, processes: [] }));
  assert.throws(() => selectOuterPidForNamespacePid({
    ...base,
    processes: [
      { pid: 4100, namespacePids: [4100, 278], namespace: 'pid:[4026533000]', cgroup: '0::/monad-tenant' },
      { pid: 4200, namespacePids: [4200, 278], namespace: 'pid:[4026533000]', cgroup: '0::/monad-tenant' },
    ],
  }));
  assert.throws(() => selectOuterPidForNamespacePid({
    ...base,
    processes: [
      { pid: 278, namespacePids: [278], namespace: 'pid:[4026533000]', cgroup: '0::/unrelated' },
    ],
  }));
  for (const process of [
    { pid: 5100, namespacePids: [5100, 278], namespace: 'pid:[4026534000]', cgroup: '0::/monad-tenant' },
    { pid: 6100, namespacePids: [6100, 88, 278], namespace: 'pid:[4026535000]', cgroup: '0::/monad-tenant' },
    { pid: 278, namespacePids: [278], namespace: 'pid:[4026531000]', cgroup: '0::/monad-tenant' },
  ]) {
    assert.throws(() => selectOuterPidForNamespacePid({ ...base, processes: [process] }));
  }
});

const rootIdentity = { uid: 0, gid: 0, groups: [0] };
const workerIdentity = { uid: 33, gid: 33, groups: [33] };

function process(overrides) {
  return {
    pid: 100,
    ppid: 50,
    executable: '/usr/sbin/nginx',
    argv: ['nginx: master process /usr/sbin/nginx -g daemon off;'],
    identity: rootIdentity,
    ...overrides,
  };
}

test('accepts the pinned nginx root master and www-data worker shape', () => {
  const input = {
    leaderPid: 100,
    processes: [
      process({}),
      process({ pid: 101, ppid: 100, identity: workerIdentity, argv: ['nginx: worker process'] }),
      process({ pid: 102, ppid: 100, identity: workerIdentity, argv: ['nginx: worker process'] }),
    ],
  };
  assert.equal(verifyPinnedNginxProcesses(input), true);
  const standalone = (0, eval)(`(${verifyPinnedNginxProcesses.toString()})`);
  assert.equal(standalone(input), true);
});

test('rejects nginx identity, command, ancestry, or missing-worker drift', () => {
  const master = process({});
  const worker = process({
    pid: 101,
    ppid: 100,
    identity: workerIdentity,
    argv: ['nginx: worker process'],
  });
  for (const processes of [
    [master],
    [process({ identity: { uid: 0, gid: 0, groups: [] } }), worker],
    [master, { ...worker, identity: { uid: 911, gid: 1001, groups: [100] } }],
    [master, { ...worker, ppid: 99 }],
    [master, { ...worker, executable: '/tmp/nginx' }],
    [master, { ...worker, argv: ['nginx: cache manager process'] }],
    [{ ...master, argv: ['/usr/sbin/nginx', '-g', 'daemon off;'] }, worker],
  ]) {
    assert.equal(verifyPinnedNginxProcesses({ leaderPid: 100, processes }), false);
  }
});

test('accepts only the pinned inert root watchdog leader with no children', () => {
  const watchdog = {
    pid: 200,
    ppid: 60,
    executable: '/usr/bin/sleep',
    argv: ['sleep', 'infinity'],
    identity: rootIdentity,
    environment: { RESTART_APP: 'false' },
  };
  assert.equal(verifyPinnedWatchdogProcesses({
    leaderPid: 200,
    processes: [watchdog],
  }), true);
  const uutilsWatchdog = {
    ...watchdog,
    executable: '/usr/lib/cargo/bin/coreutils/sleep',
  };
  assert.equal(verifyPinnedWatchdogProcesses({
    leaderPid: 200,
    processes: [uutilsWatchdog],
  }), true);
  const standalone = (0, eval)(`(${verifyPinnedWatchdogProcesses.toString()})`);
  assert.equal(standalone({ leaderPid: 200, processes: [watchdog] }), true);
  assert.equal(standalone({ leaderPid: 200, processes: [uutilsWatchdog] }), true);
  for (const processes of [
    [{ ...watchdog, identity: { uid: 0, gid: 0, groups: [] } }],
    [{ ...watchdog, executable: '/bin/bash' }],
    [{ ...watchdog, executable: '/usr/lib/cargo/bin/coreutils/timeout' }],
    [{ ...watchdog, argv: ['sleep', '30'] }],
    [{ ...watchdog, environment: { RESTART_APP: 'true' } }],
    [{ ...watchdog, environment: {} }],
    [watchdog, { ...watchdog, pid: 201, ppid: 200, identity: { uid: 911, gid: 1001, groups: [100] } }],
  ]) {
    assert.equal(verifyPinnedWatchdogProcesses({ leaderPid: 200, processes }), false);
  }
});
