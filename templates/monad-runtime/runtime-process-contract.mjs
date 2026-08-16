export function parseNamespacePidVector(raw) {
  if (typeof raw !== 'string') return [];
  const matches = [...raw.matchAll(/^NSpid:[ \t]+(.+)$/gm)];
  if (matches.length !== 1) return [];
  const values = matches[0][1].trim().split(/\s+/);
  if (
    values.length === 0 ||
    values.some((value) => !/^[1-9][0-9]*$/.test(value))
  ) {
    return [];
  }
  return values.map(Number);
}

export function selectOuterPidForNamespacePid({
  innerPid,
  expectedMembership,
  expectedNamespace,
  expectedNamespaceDepth,
  processes,
}) {
  if (
    !Number.isSafeInteger(innerPid) ||
    innerPid <= 1 ||
    typeof expectedMembership !== 'string' ||
    expectedMembership.length === 0 ||
    typeof expectedNamespace !== 'string' ||
    !/^pid:\[[1-9][0-9]*\]$/.test(expectedNamespace) ||
    !Number.isSafeInteger(expectedNamespaceDepth) ||
    expectedNamespaceDepth < 1 ||
    !Array.isArray(processes)
  ) {
    throw new Error('namespace PID selection input is invalid');
  }
  const observed = new Set();
  for (const process of processes) {
    if (
      process === null ||
      typeof process !== 'object' ||
      !Number.isSafeInteger(process.pid) ||
      process.pid <= 1 ||
      observed.has(process.pid) ||
      !Array.isArray(process.namespacePids) ||
      process.namespacePids.length === 0 ||
      process.namespacePids.some(
        (value) => !Number.isSafeInteger(value) || value <= 0,
      ) ||
      process.namespacePids[0] !== process.pid ||
      typeof process.namespace !== 'string' ||
      !/^pid:\[[1-9][0-9]*\]$/.test(process.namespace) ||
      typeof process.cgroup !== 'string'
    ) {
      throw new Error('namespace PID process evidence is invalid');
    }
    observed.add(process.pid);
  }
  const candidates = processes.filter((process) =>
    process.cgroup === expectedMembership &&
    process.namespace === expectedNamespace &&
    process.namespacePids.length === expectedNamespaceDepth &&
    process.namespacePids[process.namespacePids.length - 1] === innerPid);
  if (candidates.length === 1) return candidates[0].pid;
  if (candidates.length > 1) {
    throw new Error('namespace PID maps to multiple outer processes');
  }
  throw new Error('namespace PID has no exact outer process mapping');
}

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
