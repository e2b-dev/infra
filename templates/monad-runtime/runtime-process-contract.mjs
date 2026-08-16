export function verifyPinnedNginxProcesses({ leaderPid, processes }) {
  const sameArray = (actual, expected) =>
    Array.isArray(actual) &&
    actual.length === expected.length &&
    actual.every((value, index) => value === expected[index]);
  const exactIdentity = (actual, uid, gid, groups) =>
    actual !== null &&
    typeof actual === 'object' &&
    actual.uid === uid &&
    actual.gid === gid &&
    sameArray(actual.groups, groups);
  const exactNginxExecutable = (process) =>
    process !== null &&
    typeof process === 'object' &&
    process.executable === '/usr/sbin/nginx' &&
    Array.isArray(process.argv);
  const title = (process) => process.argv.join(' ');

  if (!Number.isSafeInteger(leaderPid) || leaderPid <= 1 || !Array.isArray(processes)) {
    return false;
  }
  if (new Set(processes.map((process) => process?.pid)).size !== processes.length) {
    return false;
  }
  const masters = processes.filter((process) => process?.pid === leaderPid);
  const workers = processes.filter((process) => process?.pid !== leaderPid);
  return masters.length === 1 &&
    exactNginxExecutable(masters[0]) &&
    title(masters[0]) ===
      'nginx: master process /usr/sbin/nginx -g daemon off;' &&
    exactIdentity(masters[0].identity, 0, 0, [0]) &&
    workers.length > 0 &&
    workers.every((worker) =>
      Number.isSafeInteger(worker?.pid) &&
      worker.pid > 1 &&
      worker.ppid === leaderPid &&
      exactNginxExecutable(worker) &&
      title(worker) === 'nginx: worker process' &&
      exactIdentity(worker.identity, 33, 33, [33]));
}

export function verifyPinnedWatchdogProcesses({ leaderPid, processes }) {
  const sameArray = (actual, expected) =>
    Array.isArray(actual) &&
    actual.length === expected.length &&
    actual.every((value, index) => value === expected[index]);
  const exactIdentity = (actual) =>
    actual !== null &&
    typeof actual === 'object' &&
    actual.uid === 0 &&
    actual.gid === 0 &&
    sameArray(actual.groups, [0]);
  if (
    !Number.isSafeInteger(leaderPid) ||
    leaderPid <= 1 ||
    !Array.isArray(processes) ||
    processes.length !== 1
  ) {
    return false;
  }
  const [leader] = processes;
  return leader?.pid === leaderPid &&
    leader.executable === '/usr/bin/sleep' &&
    sameArray(leader.argv, ['sleep', 'infinity']) &&
    leader.environment?.RESTART_APP === 'false' &&
    exactIdentity(leader.identity);
}
