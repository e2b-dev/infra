import assert from 'node:assert/strict';
import test from 'node:test';

import {
  verifyPinnedNginxProcesses,
  verifyPinnedWatchdogProcesses,
} from './runtime-process-contract.mjs';

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
  const standalone = (0, eval)(`(${verifyPinnedWatchdogProcesses.toString()})`);
  assert.equal(standalone({ leaderPid: 200, processes: [watchdog] }), true);
  for (const processes of [
    [{ ...watchdog, identity: { uid: 0, gid: 0, groups: [] } }],
    [{ ...watchdog, executable: '/bin/bash' }],
    [{ ...watchdog, argv: ['sleep', '30'] }],
    [{ ...watchdog, environment: { RESTART_APP: 'true' } }],
    [{ ...watchdog, environment: {} }],
    [watchdog, { ...watchdog, pid: 201, ppid: 200, identity: { uid: 911, gid: 1001, groups: [100] } }],
  ]) {
    assert.equal(verifyPinnedWatchdogProcesses({ leaderPid: 200, processes }), false);
  }
});
