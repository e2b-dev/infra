const RETRYABLE_STAGES = new Set([
  'marker',
  'marker_daemon_proc_root_run_missing',
  'marker_daemon_directory_missing',
  'marker_daemon_file_missing',
  'marker_supervisor_proc_root_run_missing',
  'marker_supervisor_directory_missing',
  'marker_supervisor_file_missing',
  'supervisor',
  'service_mapping',
  'process_attestation',
  'filesystem_attestation',
]);
const TERMINAL_STAGES = new Set([
  'probe_identity',
  'daemon_service_mapping',
  'daemon_supervisor_mount_namespace',
  'daemon_supervisor_root_identity',
  'daemon_supervisor_run_identity',
  'daemon_supervisor_filesystem_stability',
  'marker_binding',
  'marker_target',
  ...['daemon', 'supervisor'].flatMap((authority) => [
    `marker_${authority}_proc_root_run_denied`,
    `marker_${authority}_proc_root_run_error`,
    `marker_${authority}_proc_root_run_type`,
    `marker_${authority}_proc_root_run_owner`,
    `marker_${authority}_directory_denied`,
    `marker_${authority}_directory_error`,
    `marker_${authority}_directory_type`,
    `marker_${authority}_directory_owner`,
    `marker_${authority}_directory_mode`,
    `marker_${authority}_file_denied`,
    `marker_${authority}_file_error`,
    `marker_${authority}_file_type`,
    `marker_${authority}_file_owner`,
    `marker_${authority}_file_mode`,
  ]),
]);

// Evidence must converge before the final transport window so a safe terminal
// stage can cross the E2B command stream within the original total deadline.
const TENANT_BOUNDARY_TOTAL_TIMEOUT_MS = 180_000;
const TENANT_BOUNDARY_TRANSPORT_HEADROOM_MS = 5_000;
const TENANT_BOUNDARY_MAX_PROBE_EXECUTION_MS = 30_000;

export const TENANT_BOUNDARY_CONVERGENCE_TIMEOUT_MS =
  TENANT_BOUNDARY_TOTAL_TIMEOUT_MS - TENANT_BOUNDARY_TRANSPORT_HEADROOM_MS;

export function tenantBoundaryProbeTimeoutMs(remainingMs) {
  if (
    !Number.isSafeInteger(remainingMs) ||
    remainingMs <= 0 ||
    remainingMs > TENANT_BOUNDARY_CONVERGENCE_TIMEOUT_MS
  ) {
    throw new Error('tenant boundary probe remaining time is invalid');
  }
  return Math.min(TENANT_BOUNDARY_MAX_PROBE_EXECUTION_MS, remainingMs) +
    TENANT_BOUNDARY_TRANSPORT_HEADROOM_MS;
}

export function classifyTenantBoundaryProbeIdentity({ getuid, getgid }) {
  try {
    if (getuid() === 0 && getgid() === 0) {
      return { probe_ok: true };
    }
  } catch {}
  return { probe_ok: false, stage: 'probe_identity' };
}

export function classifyTenantBoundaryTopology({
  daemonPid,
  supervisorPid,
  daemonServicePid,
  daemonNamespacePids,
  supervisorNamespacePids,
  daemonNamespace,
  supervisorNamespace,
  daemonMountNamespace,
  supervisorMountNamespace,
  daemonRoot,
  supervisorRoot,
  daemonRun,
  supervisorRun,
}) {
  const failure = (stage) => ({ probe_ok: false, stage });
  const canonicalVector = (value, outerPid) =>
    Array.isArray(value) &&
    value.length > 0 &&
    value.every((pid) => Number.isSafeInteger(pid) && pid > 0) &&
    value[0] === outerPid;
  const exactIdentity = (left, right) => {
    const validValue = (value) =>
      (Number.isSafeInteger(value) && value >= 0) ||
      (typeof value === 'bigint' && value >= 0n);
    return left !== null && typeof left === 'object' && !Array.isArray(left) &&
      right !== null && typeof right === 'object' && !Array.isArray(right) &&
      validValue(left.dev) && validValue(left.ino) &&
      validValue(right.dev) && validValue(right.ino) &&
      left.dev === right.dev && left.ino === right.ino;
  };
  if (
    !Number.isSafeInteger(daemonPid) || daemonPid <= 1 ||
    !Number.isSafeInteger(supervisorPid) || supervisorPid <= 1 ||
    !Number.isSafeInteger(daemonServicePid) || daemonServicePid <= 0 ||
    !canonicalVector(daemonNamespacePids, daemonPid) ||
    !canonicalVector(supervisorNamespacePids, supervisorPid) ||
    daemonNamespacePids.length !== supervisorNamespacePids.length ||
    daemonNamespacePids[daemonNamespacePids.length - 1] !== daemonServicePid ||
    typeof daemonNamespace !== 'string' || daemonNamespace.length === 0 ||
    daemonNamespace !== supervisorNamespace
  ) {
    return failure('daemon_service_mapping');
  }
  if (
    typeof daemonMountNamespace !== 'string' ||
    daemonMountNamespace.length === 0 ||
    daemonMountNamespace !== supervisorMountNamespace
  ) {
    return failure('daemon_supervisor_mount_namespace');
  }
  if (!exactIdentity(daemonRoot, supervisorRoot)) {
    return failure('daemon_supervisor_root_identity');
  }
  if (!exactIdentity(daemonRun, supervisorRun)) {
    return failure('daemon_supervisor_run_identity');
  }
  return { probe_ok: true };
}

export function classifyTenantBoundaryFilesystemStability({
  initialDaemonRoot,
  initialSupervisorRoot,
  initialDaemonRun,
  initialSupervisorRun,
  finalDaemonRoot,
  finalSupervisorRoot,
  finalDaemonRun,
  finalSupervisorRun,
}) {
  const exactIdentity = (left, right) => {
    const validValue = (value) =>
      (Number.isSafeInteger(value) && value >= 0) ||
      (typeof value === 'bigint' && value >= 0n);
    return left !== null && typeof left === 'object' && !Array.isArray(left) &&
      right !== null && typeof right === 'object' && !Array.isArray(right) &&
      validValue(left.dev) && validValue(left.ino) &&
      validValue(right.dev) && validValue(right.ino) &&
      left.dev === right.dev && left.ino === right.ino;
  };
  const stable =
    exactIdentity(initialDaemonRoot, initialSupervisorRoot) &&
    exactIdentity(initialDaemonRoot, finalDaemonRoot) &&
    exactIdentity(initialDaemonRoot, finalSupervisorRoot) &&
    exactIdentity(initialDaemonRun, initialSupervisorRun) &&
    exactIdentity(initialDaemonRun, finalDaemonRun) &&
    exactIdentity(initialDaemonRun, finalSupervisorRun);
  return stable
    ? { probe_ok: true }
    : {
        probe_ok: false,
        stage: 'daemon_supervisor_filesystem_stability',
      };
}

export function inspectTenantBoundaryMarkerFilesystem({
  authority,
  lstat,
  procRootRun,
  admissionRoot,
  marker,
}) {
  if (authority !== 'daemon' && authority !== 'supervisor') {
    throw new Error('tenant boundary marker authority is invalid');
  }
  const failure = (stage) => ({ probe_ok: false, stage });
  const inspect = ({
    path,
    target,
    expectedType,
    typeStage,
    ownerStage,
    mode,
    modeStage,
  }) => {
    let metadata;
    try {
      metadata = lstat(path);
    } catch (error) {
      const code = error !== null && typeof error === 'object'
        ? error.code
        : undefined;
      const category = code === 'ENOENT'
        ? 'missing'
        : code === 'EACCES' || code === 'EPERM'
          ? 'denied'
          : 'error';
      return failure(`marker_${authority}_${target}_${category}`);
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
      target: 'proc_root_run',
      expectedType: 'directory',
      typeStage: `marker_${authority}_proc_root_run_type`,
      ownerStage: `marker_${authority}_proc_root_run_owner`,
    },
    {
      path: admissionRoot,
      target: 'directory',
      expectedType: 'directory',
      typeStage: `marker_${authority}_directory_type`,
      ownerStage: `marker_${authority}_directory_owner`,
      mode: 0o700,
      modeStage: `marker_${authority}_directory_mode`,
    },
    {
      path: marker,
      target: 'file',
      expectedType: 'file',
      typeStage: `marker_${authority}_file_type`,
      ownerStage: `marker_${authority}_file_owner`,
      mode: 0o444,
      modeStage: `marker_${authority}_file_mode`,
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
    'daemon_service_mapping',
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
    let record;
    try {
      record = await probe({ remainingMs: probeBudget });
    } catch (error) {
      const afterFailure = now();
      if (!Number.isFinite(afterFailure)) {
        throw new Error('tenant boundary convergence clock is invalid');
      }
      if (deadline - afterFailure <= 0) {
        throw new TenantBoundaryConvergenceError(lastStage);
      }
      throw error;
    }
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
