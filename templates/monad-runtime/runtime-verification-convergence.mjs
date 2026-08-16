const RETRYABLE_STAGES = new Set([
  'marker',
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
  'supervisor',
  'service_mapping',
  'process_attestation',
  'filesystem_attestation',
]);
const TERMINAL_STAGES = new Set(['probe_identity']);

export function classifyTenantBoundaryProbeIdentity({ getuid, getgid }) {
  try {
    if (getuid() === 0 && getgid() === 0) {
      return { probe_ok: true };
    }
  } catch {}
  return { probe_ok: false, stage: 'probe_identity' };
}

export function inspectTenantBoundaryMarkerFilesystem({
  lstat,
  procRootRun,
  admissionRoot,
  marker,
}) {
  const failure = (stage) => ({ probe_ok: false, stage });
  const inspect = ({
    path,
    accessStage,
    expectedType,
    typeStage,
    ownerStage,
    mode,
    modeStage,
  }) => {
    let metadata;
    try {
      metadata = lstat(path);
    } catch {
      return failure(accessStage);
    }
    try {
      const typeMatches = expectedType === 'directory'
        ? metadata.isDirectory()
        : metadata.isFile();
      if (!typeMatches || metadata.isSymbolicLink()) {
        return failure(typeStage);
      }
    } catch {
      return failure(typeStage);
    }
    try {
      if (metadata.uid !== 0 || metadata.gid !== 0) {
        return failure(ownerStage);
      }
    } catch {
      return failure(ownerStage);
    }
    if (mode !== undefined) {
      try {
        if ((metadata.mode & 0o7777) !== mode) {
          return failure(modeStage);
        }
      } catch {
        return failure(modeStage);
      }
    }
    return null;
  };
  const checks = [
    {
      path: procRootRun,
      accessStage: 'marker_proc_root_run_access',
      expectedType: 'directory',
      typeStage: 'marker_proc_root_run_type',
      ownerStage: 'marker_proc_root_run_owner',
    },
    {
      path: admissionRoot,
      accessStage: 'marker_directory_access',
      expectedType: 'directory',
      typeStage: 'marker_directory_type',
      ownerStage: 'marker_directory_owner',
      mode: 0o700,
      modeStage: 'marker_directory_mode',
    },
    {
      path: marker,
      accessStage: 'marker_file_access',
      expectedType: 'file',
      typeStage: 'marker_file_type',
      ownerStage: 'marker_file_owner',
      mode: 0o444,
      modeStage: 'marker_file_mode',
    },
  ];
  for (const check of checks) {
    const result = inspect(check);
    if (result !== null) return result;
  }
  return { probe_ok: true };
}

export function bindTenantBoundaryMarker(daemonMembership, marker) {
  if (
    typeof daemonMembership !== 'string' ||
    typeof marker !== 'string' ||
    !daemonMembership.startsWith('0::/') ||
    daemonMembership.includes('\n') ||
    daemonMembership.includes('\r') ||
    daemonMembership.includes('\0')
  ) {
    throw new Error('tenant boundary marker binding is invalid');
  }
  const relative = daemonMembership.slice(3);
  const segments = relative.split('/').slice(1);
  if (
    relative !== '/' &&
    (segments.length === 0 || segments.some(
      (segment) =>
        segment === '' ||
        segment === '.' ||
        segment === '..' ||
        /[\u0000-\u001f\u007f]/.test(segment),
    ))
  ) {
    throw new Error('tenant boundary marker binding is invalid');
  }
  const daemonCgroup = relative === '/'
    ? '/sys/fs/cgroup'
    : `/sys/fs/cgroup${relative}`;
  const tenantCgroup = `${daemonCgroup}/monad-tenant`;
  if (marker !== `${tenantCgroup}\n`) {
    throw new Error('tenant boundary marker binding is invalid');
  }
  return {
    tenantCgroup,
    expectedMembership: `0::${relative === '/' ? '' : relative}/monad-tenant`,
  };
}

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
    'marker_parent_daemon_cgroup_match',
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
      (!RETRYABLE_STAGES.has(record.stage) &&
        !TERMINAL_STAGES.has(record.stage))
    ) {
      throw new Error('tenant boundary probe returned an invalid record');
    }
    if (TERMINAL_STAGES.has(record.stage)) {
      throw new TenantBoundaryConvergenceError(record.stage);
    }
    lastStage = record.stage;
    await sleep(Math.min(intervalMs, remaining));
  }
}
