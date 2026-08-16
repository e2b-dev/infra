import { execFileSync, spawnSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { isAbsolute } from 'node:path';

const PLATFORM = 'linux/amd64';
const MAX_DOCKER_LOG_BYTES = 64 * 1024;
const MAX_FATAL_NAME_LENGTH = 80;
const MAX_FATAL_MESSAGE_LENGTH = 240;
const SAFE_FATAL_NAMES = new Set([
  'AggregateError',
  'Error',
  'RangeError',
  'ReferenceError',
  'SyntaxError',
  'SystemError',
  'TypeError',
]);

const launchDaemon = String.raw`install -d -o root -g root -m 0755 /opt/monad/runtime/bin /usr/local/libexec /etc/monad
test -d /opt/monad/runtime/bin
test ! -L /opt/monad/runtime/bin
test "$(stat -c '%u:%g:%a' -- /opt/monad/runtime/bin)" = 0:0:755
install -o root -g root -m 0755 /opt/monad-preflight/assets/monad-agent /opt/monad/runtime/bin/monad-agent
install -o root -g root -m 0755 /opt/monad-preflight/assets/monad-tenant-admission /usr/local/libexec/monad-tenant-admission
install -o root -g root -m 0444 /opt/monad-preflight/assets/session-rebind-tenant-boundary.json /etc/monad/session-rebind-tenant-boundary.json
install -o root -g root -m 0755 /opt/monad-preflight/assets/entrypoint.sh /opt/monad/runtime/bin/monad-entrypoint
exec /bin/bash /opt/monad-preflight/svc-monad-agent-run`;

function buildEvidenceProbe(daemonSha256, entrypointSha256, admissionHelperSha256) {
  return String.raw`admission_root=/run/monad-admission
marker="$admission_root/tenant-cgroup-ready"
socket=/var/run/monad/credential-bootstrap.sock
daemon_directory=/opt/monad/runtime/bin
launcher=/opt/monad/runtime/bin/monad-agent
entrypoint=/opt/monad/runtime/bin/monad-entrypoint
helper=/usr/local/libexec/monad-tenant-admission
expected_daemon_sha256='${daemonSha256}'
expected_entrypoint_sha256='${entrypointSha256}'
expected_helper_sha256='${admissionHelperSha256}'
daemon_pid=
for comm_path in /proc/[0-9]*/comm; do
  test -r "$comm_path" || continue
  test "$(cat "$comm_path")" = monad-agent || continue
  candidate="$(basename "$(dirname "$comm_path")")"
  test -z "$daemon_pid" || exit 1
  daemon_pid="$candidate"
done
case "$daemon_pid" in ''|*[!0-9]*) exit 1 ;; esac
test "$daemon_pid" -gt 1
IFS= read -r -d '' first_argv < "/proc/$daemon_pid/cmdline"
test "$first_argv" = "$launcher"
test -d "$daemon_directory"
test ! -L "$daemon_directory"
test "$(stat -c '%u:%g:%a' -- "$daemon_directory")" = 0:0:755
test -f "$launcher"
test ! -L "$launcher"
test "$(stat -c '%u:%g:%a' -- "$launcher")" = 0:0:755
test "$(sha256sum "$launcher" | cut -d ' ' -f 1)" = "$expected_daemon_sha256"
test -f "$entrypoint"
test ! -L "$entrypoint"
test "$(stat -c '%u:%g:%a' -- "$entrypoint")" = 0:0:755
test "$(sha256sum "$entrypoint" | cut -d ' ' -f 1)" = "$expected_entrypoint_sha256"
test -f "$helper"
test ! -L "$helper"
test "$(stat -c '%u:%g:%a' -- "$helper")" = 0:0:755
test "$(sha256sum /usr/local/libexec/monad-tenant-admission | cut -d ' ' -f 1)" = "$expected_helper_sha256"
test -d "$admission_root"
test ! -L "$admission_root"
test "$(stat -c '%u:%g:%a' -- "$admission_root")" = 0:0:700
test -f "$marker"
test ! -L "$marker"
test "$(stat -c '%u:%g:%a' -- "$marker")" = 0:0:444
test "$(grep -c '^0::/' "/proc/$daemon_pid/cgroup")" = 1
membership="$(cat "/proc/$daemon_pid/cgroup")"
case "$membership" in
  0::/) expected=/sys/fs/cgroup/monad-tenant ;;
  0::/*)
    membership_path="$(printf '%s' "$membership" | cut -c4-)"
    expected="/sys/fs/cgroup$membership_path/monad-tenant"
    ;;
  *) exit 1 ;;
esac
test -d "$expected"
test ! -L "$expected"
actual_hex="$(od -An -tx1 "$marker" | tr -d ' \n')"
expected_hex="$(printf '%s\n' "$expected" | od -An -tx1 | tr -d ' \n')"
test "$actual_hex" = "$expected_hex"
test -S "$socket"
test ! -L "$socket"
test "$(stat -c '%u:%g:%a' -- "$socket")" = 0:0:600
printf '%s' "$expected"`;
}

function defaultRunDocker(args) {
  return execFileSync('docker', args, {
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
  }).trim();
}

function defaultReadContainerLogs(containerName) {
  const result = spawnSync('docker', ['logs', '--tail', '64', containerName], {
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
    maxBuffer: MAX_DOCKER_LOG_BYTES,
    timeout: 5_000,
  });
  return `${result.stdout ?? ''}\n${result.stderr ?? ''}`.slice(-MAX_DOCKER_LOG_BYTES);
}

function boundedDiagnosticField(value, maxLength) {
  if (typeof value !== 'string') return null;
  const normalized = value.replace(/[\p{Cc}\p{Cf}]+/gu, ' ').trim();
  return normalized.length > 0 ? normalized.slice(0, maxLength) : null;
}

function parseBootFatal(logs) {
  if (typeof logs !== 'string') return null;
  for (const line of logs.slice(-MAX_DOCKER_LOG_BYTES).split('\n').reverse()) {
    if (line.length === 0 || line.length > 16 * 1024) continue;
    let record;
    try {
      record = JSON.parse(line);
    } catch {
      continue;
    }
    if (
      record === null ||
      typeof record !== 'object' ||
      Array.isArray(record) ||
      record.level !== 'error' ||
      record.msg !== '[boot] fatal' ||
      record.error === null ||
      typeof record.error !== 'object' ||
      Array.isArray(record.error)
    ) {
      continue;
    }
    const boundedName = boundedDiagnosticField(record.error.name, MAX_FATAL_NAME_LENGTH);
    if (!boundedName) continue;
    const name = SAFE_FATAL_NAMES.has(boundedName)
      ? boundedName
      : '[redacted]';
    const message = boundedDiagnosticField(record.error.message, MAX_FATAL_MESSAGE_LENGTH);
    if (name && message) return { name, message };
  }
  return null;
}

function bindMount(source, target) {
  return `type=bind,source=${source},target=${target},readonly`;
}

function validateOptions({
  assetsDir,
  serviceRunPath,
  baseImage,
  runtimeVersion,
  daemonSha256,
  entrypointSha256,
  admissionHelperSha256,
  containerName,
  runDocker,
  readContainerLogs,
  now,
  sleep,
  timeoutMs,
  intervalMs,
}) {
  const exactHash = (value) => typeof value === 'string' && /^[a-f0-9]{64}$/.test(value);
  const exactPath = (value) =>
    typeof value === 'string' && isAbsolute(value) && !value.includes('\0');
  if (
    !exactPath(assetsDir) ||
    !exactPath(serviceRunPath) ||
    typeof baseImage !== 'string' ||
    !/@sha256:[a-f0-9]{64}$/.test(baseImage) ||
    !exactHash(runtimeVersion) ||
    !exactHash(daemonSha256) ||
    !exactHash(entrypointSha256) ||
    !exactHash(admissionHelperSha256) ||
    typeof containerName !== 'string' ||
    !/^monad-runtime-preflight-[a-z0-9-]{1,80}$/.test(containerName) ||
    typeof runDocker !== 'function' ||
    typeof readContainerLogs !== 'function' ||
    typeof now !== 'function' ||
    typeof sleep !== 'function' ||
    !Number.isSafeInteger(timeoutMs) || timeoutMs <= 0 || timeoutMs > 120_000 ||
    !Number.isSafeInteger(intervalMs) || intervalMs <= 0 || intervalMs > timeoutMs
  ) {
    throw new Error('native amd64 runtime preflight options are invalid');
  }
}

function canonicalMarkerPath(value) {
  return typeof value === 'string' &&
    /^\/sys\/fs\/cgroup(?:\/[A-Za-z0-9_.:-]+)*\/monad-tenant$/.test(value) &&
    !value.split('/').some((segment) => segment === '.' || segment === '..');
}

export function validateNativeAmd64RuntimePreflightEvidence(evidence, {
  daemonSha256,
  entrypointSha256,
  admissionHelperSha256,
} = {}) {
  const expectedKeys = [
    'platform',
    'cgroup_namespace',
    'network',
    'daemon_directory_metadata',
    'admission_root_mode',
    'marker_mode',
    'marker_path',
    'bootstrap_socket_mode',
    'credential_bootstrap',
    'daemon_sha256',
    'entrypoint_sha256',
    'tenant_admission_helper_sha256',
  ].sort();
  const keys = evidence !== null && typeof evidence === 'object' &&
      !Array.isArray(evidence)
    ? Object.keys(evidence).sort()
    : [];
  const valid =
    keys.length === expectedKeys.length &&
    keys.every((key, index) => key === expectedKeys[index]) &&
    evidence.platform === PLATFORM &&
    evidence.cgroup_namespace === 'private' &&
    evidence.network === 'none' &&
    evidence.daemon_directory_metadata === '0:0:755' &&
    evidence.admission_root_mode === '700' &&
    evidence.marker_mode === '444' &&
    canonicalMarkerPath(evidence.marker_path) &&
    evidence.bootstrap_socket_mode === '600' &&
    evidence.credential_bootstrap === 'awaiting' &&
    typeof daemonSha256 === 'string' &&
    /^[a-f0-9]{64}$/.test(daemonSha256) &&
    typeof entrypointSha256 === 'string' &&
    /^[a-f0-9]{64}$/.test(entrypointSha256) &&
    typeof admissionHelperSha256 === 'string' &&
    /^[a-f0-9]{64}$/.test(admissionHelperSha256) &&
    evidence.daemon_sha256 === daemonSha256 &&
    evidence.entrypoint_sha256 === entrypointSha256 &&
    evidence.tenant_admission_helper_sha256 === admissionHelperSha256;
  if (!valid) {
    throw new Error('native amd64 runtime preflight evidence is invalid');
  }
  return evidence;
}

export async function runNativeAmd64RuntimePreflight({
  assetsDir,
  serviceRunPath,
  baseImage,
  runtimeVersion,
  daemonSha256,
  entrypointSha256,
  admissionHelperSha256,
  containerName = `monad-runtime-preflight-${runtimeVersion?.slice(0, 12)}-${randomUUID()}`,
  runDocker = defaultRunDocker,
  readContainerLogs = defaultReadContainerLogs,
  now = Date.now,
  sleep = (milliseconds) =>
    new Promise((resolve) => setTimeout(resolve, milliseconds)),
  timeoutMs = 30_000,
  intervalMs = 500,
} = {}) {
  validateOptions({
    assetsDir,
    serviceRunPath,
    baseImage,
    runtimeVersion,
    daemonSha256,
    entrypointSha256,
    admissionHelperSha256,
    containerName,
    runDocker,
    readContainerLogs,
    now,
    sleep,
    timeoutMs,
    intervalMs,
  });

  const dockerPlatform = runDocker([
    'info',
    '--format',
    '{{.OSType}}/{{.Architecture}}',
  ]);
  if (dockerPlatform !== 'linux/amd64' && dockerPlatform !== 'linux/x86_64') {
    throw new Error('runtime preflight requires a native linux/amd64 Docker host');
  }

  const createArgs = [
    'create',
    '--name', containerName,
    '--platform', PLATFORM,
    '--init',
    '--privileged',
    '--cgroupns', 'private',
    '--network', 'none',
    '--user', 'root',
    '--env', 'MONAD_TENANT_BOUNDARY_REQUIRED=1',
    '--env', 'MONAD_CREDENTIAL_BOOTSTRAP_REQUIRED=1',
    '--env', 'MONAD_WORKSPACE=/workspace',
    '--mount', bindMount(assetsDir, '/opt/monad-preflight/assets'),
    '--mount', bindMount(serviceRunPath, '/opt/monad-preflight/svc-monad-agent-run'),
    '--entrypoint', '/bin/bash',
    baseImage,
    '-ceu',
    launchDaemon,
  ];

  let created = false;
  try {
    runDocker(createArgs);
    created = true;
    runDocker(['start', containerName]);
    const deadline = now() + timeoutMs;
    while (now() < deadline) {
      let markerPath;
      try {
        markerPath = runDocker([
          'exec',
          containerName,
          '/bin/bash',
          '-ceu',
          buildEvidenceProbe(daemonSha256, entrypointSha256, admissionHelperSha256),
        ]);
      } catch {
        let running = false;
        let exitCode = null;
        let stateKnown = false;
        try {
          const state = runDocker([
            'container',
            'inspect',
            '--format',
            '{{.State.Running}} {{.State.ExitCode}}',
            containerName,
          ]);
          const stopped = /^false (0|[1-9][0-9]{0,2})$/.exec(state);
          if (state === 'true 0') {
            stateKnown = true;
            running = true;
          } else if (stopped) {
            const parsedExitCode = Number.parseInt(stopped[1], 10);
            if (parsedExitCode <= 255) {
              stateKnown = true;
              exitCode = parsedExitCode;
            }
          }
        } catch {}
        if (!stateKnown) {
          throw new Error('native amd64 runtime preflight container state is unavailable');
        }
        if (!running) {
          let fatal = null;
          try {
            fatal = parseBootFatal(readContainerLogs(containerName));
          } catch {}
          const diagnostic = {
            exit_code: exitCode,
            ...(fatal ? { fatal } : {}),
          };
          throw new Error(
            `native amd64 runtime preflight daemon exited before evidence: ${JSON.stringify(diagnostic)}`,
          );
        }
        await sleep(Math.min(intervalMs, Math.max(0, deadline - now())));
        continue;
      }
      if (!canonicalMarkerPath(markerPath)) {
        throw new Error('native amd64 runtime preflight returned invalid marker evidence');
      }
      return validateNativeAmd64RuntimePreflightEvidence({
        platform: PLATFORM,
        cgroup_namespace: 'private',
        network: 'none',
        daemon_directory_metadata: '0:0:755',
        admission_root_mode: '700',
        marker_mode: '444',
        marker_path: markerPath,
        bootstrap_socket_mode: '600',
        credential_bootstrap: 'awaiting',
        daemon_sha256: daemonSha256,
        entrypoint_sha256: entrypointSha256,
        tenant_admission_helper_sha256: admissionHelperSha256,
      }, { daemonSha256, entrypointSha256, admissionHelperSha256 });
    }
    throw new Error(
      'native amd64 runtime preflight did not publish exact credential-free evidence',
    );
  } finally {
    if (created) {
      const removed = runDocker(['container', 'rm', '--force', containerName]);
      if (removed !== containerName) {
        throw new Error('native amd64 runtime preflight cleanup was not exact');
      }
    }
  }
}
