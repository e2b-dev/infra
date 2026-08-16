const RETRYABLE_STAGES = new Set([
  'marker',
  'marker_file',
  'marker_target',
  'supervisor',
  'service_mapping',
  'process_attestation',
  'filesystem_attestation',
]);

export function tenantBoundaryProcRootPaths(supervisorPid) {
  if (!Number.isSafeInteger(supervisorPid) || supervisorPid <= 1) {
    throw new Error('tenant boundary supervisor PID is invalid');
  }
  const root = `/proc/${supervisorPid}/root`;
  const admissionRoot = `${root}/run/monad-admission`;
  return {
    root,
    admissionRoot,
    marker: `${admissionRoot}/tenant-cgroup-ready`,
    tenantCgroup: `${root}/sys/fs/cgroup/monad-tenant`,
  };
}

export function tenantBoundaryRuntimePath(supervisorPid, runtimePath) {
  if (!Number.isSafeInteger(supervisorPid) || supervisorPid <= 1) {
    throw new Error('tenant boundary supervisor PID is invalid');
  }
  if (
    typeof runtimePath !== 'string' ||
    !runtimePath.startsWith('/') ||
    runtimePath.startsWith('//') ||
    runtimePath.includes('\0') ||
    runtimePath.split('/').slice(1).some(
      (segment) => segment === '' || segment === '.' || segment === '..',
    )
  ) {
    throw new Error('tenant boundary runtime path is invalid');
  }
  return `/proc/${supervisorPid}/root${runtimePath}`;
}

export function tenantBoundaryRuntimePathChain(supervisorPid, runtimePath) {
  if (!Number.isSafeInteger(supervisorPid) || supervisorPid <= 1) {
    throw new Error('tenant boundary supervisor PID is invalid');
  }
  if (
    typeof runtimePath !== 'string' ||
    !runtimePath.startsWith('/') ||
    runtimePath.startsWith('//') ||
    runtimePath.includes('\0') ||
    runtimePath.split('/').slice(1).some(
      (segment) => segment === '' || segment === '.' || segment === '..',
    )
  ) {
    throw new Error('tenant boundary runtime path is invalid');
  }
  const chain = [];
  let current = `/proc/${supervisorPid}/root`;
  for (const segment of runtimePath.split('/').slice(1)) {
    current += `/${segment}`;
    chain.push(current);
  }
  return chain;
}

function hasExactKeys(value, expected) {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    return false;
  }
  const actual = Object.keys(value).sort();
  const wanted = [...expected].sort();
  return actual.length === wanted.length &&
    actual.every((key, index) => key === wanted[index]);
}

export function classifyTenantBoundaryEvidence(evidence) {
  const processKeys = [
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
  const filesystemKeys = [
    'daemon_executable_match',
    'attestation_hash_match',
    'attestation_identity_match',
    'attestation_files_exact',
    'marker_exact',
    'marker_basename_match',
    'marker_direct_parent_match',
  ];
  if (
    evidence === null ||
    typeof evidence !== 'object' ||
    Array.isArray(evidence) ||
    !processKeys.every((key) => evidence[key] === true)
  ) {
    return { probe_ok: false, stage: 'process_attestation' };
  }
  if (!filesystemKeys.every((key) => evidence[key] === true)) {
    return { probe_ok: false, stage: 'filesystem_attestation' };
  }
  return { probe_ok: true, evidence };
}

export class TenantBoundaryConvergenceError extends Error {
  constructor(stage) {
    super(`tenant boundary did not converge at ${stage}`);
    this.name = 'TenantBoundaryConvergenceError';
    this.stage = stage;
  }
}

export async function waitForTenantBoundaryEvidence({
  probe,
  now = Date.now,
  sleep = (milliseconds) =>
    new Promise((resolve) => setTimeout(resolve, milliseconds)),
  timeoutMs = 180_000,
  intervalMs = 2_000,
}) {
  if (
    typeof probe !== 'function' ||
    typeof now !== 'function' ||
    typeof sleep !== 'function' ||
    !Number.isSafeInteger(timeoutMs) ||
    timeoutMs <= 0 ||
    timeoutMs > 10 * 60_000 ||
    !Number.isSafeInteger(intervalMs) ||
    intervalMs <= 0 ||
    intervalMs > timeoutMs
  ) {
    throw new Error('tenant boundary convergence options are invalid');
  }
  const startedAt = now();
  if (!Number.isFinite(startedAt)) {
    throw new Error('tenant boundary convergence clock is invalid');
  }
  const deadline = startedAt + timeoutMs;
  let lastStage = 'marker';
  while (true) {
    const beforeProbe = now();
    if (!Number.isFinite(beforeProbe)) {
      throw new Error('tenant boundary convergence clock is invalid');
    }
    const probeBudget = deadline - beforeProbe;
    if (probeBudget <= 0) {
      throw new TenantBoundaryConvergenceError(lastStage);
    }
    const record = await probe({ remainingMs: probeBudget });
    const afterProbe = now();
    if (!Number.isFinite(afterProbe)) {
      throw new Error('tenant boundary convergence clock is invalid');
    }
    const remaining = deadline - afterProbe;
    if (remaining <= 0) {
      throw new TenantBoundaryConvergenceError(lastStage);
    }
    if (
      hasExactKeys(record, ['probe_ok', 'evidence']) &&
      record.probe_ok === true &&
      record.evidence !== null &&
      typeof record.evidence === 'object' &&
      !Array.isArray(record.evidence)
    ) {
      return record.evidence;
    }
    if (
      !hasExactKeys(record, ['probe_ok', 'stage']) ||
      record.probe_ok !== false ||
      typeof record.stage !== 'string' ||
      !RETRYABLE_STAGES.has(record.stage)
    ) {
      throw new Error('tenant boundary probe returned an invalid record');
    }
    lastStage = record.stage;
    await sleep(Math.min(intervalMs, remaining));
  }
}
