import { randomUUID } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { Sandbox } from 'e2b';
import {
  normalizeApiUrl,
  requiredEnv,
  safeErrorMessage,
  validateTemplateRef,
} from './runtime-core.mjs';
import {
  parseNamespacePidVector,
  selectOuterPidForNamespacePid,
  verifyPinnedNginxProcesses,
  verifyPinnedWatchdogProcesses,
} from './runtime-process-contract.mjs';
import {
  assertSafeVerificationBaseline,
  buildCleanupEvidence,
  RUNTIME_VERIFICATION_METADATA_KEY,
  RUNTIME_VERIFICATION_RUN_METADATA_KEY,
  summarizeSandboxInventory,
  SYNTHETIC_METADATA_KEY,
} from './runtime-verification-inventory.mjs';
import {
  bindTenantBoundaryMarker,
  classifyTenantBoundaryFilesystemStability,
  classifyTenantBoundaryProbeIdentity,
  classifyTenantBoundaryEvidence,
  classifyTenantBoundaryTopology,
  inspectTenantBoundaryMarkerFilesystem,
  tenantBoundaryProcRootPaths,
  tenantBoundaryRuntimePath,
  tenantBoundaryRuntimePathChain,
  waitForTenantBoundaryEvidence,
} from './runtime-verification-convergence.mjs';

const environment = process.env;
const apiKey = requiredEnv(environment, 'E2B_API_KEY');
const apiUrl = normalizeApiUrl(requiredEnv(environment, 'E2B_API_URL'));
const domain = requiredEnv(environment, 'E2B_DOMAIN');
const templateRef = validateTemplateRef(
  requiredEnv(environment, 'E2B_TEMPLATE_REF'),
);
const manifest = JSON.parse(
  await readFile(new URL('./.build-assets/manifest.json', import.meta.url)),
);
const requestTimeoutMs = 10 * 60 * 1000;
const connection = { apiKey, apiUrl, domain, requestTimeoutMs };
const verificationRunId = randomUUID();
const evidence = {
  template_ref: templateRef,
  runtime_version: manifest.runtime_version,
  verification_run_id: verificationRunId,
  started_at: new Date().toISOString(),
};
let sandbox;
let baseline;

async function listSandboxes(metadata) {
  const paginator = Sandbox.list({
    ...connection,
    limit: 100,
    query: {
      state: ['running', 'paused'],
      ...(metadata ? { metadata } : {}),
    },
  });
  const items = [];
  while (paginator.hasNext) {
    items.push(...(await paginator.nextItems(connection)));
    if (items.length > 100) {
      throw new Error('sandbox inventory exceeded the 100-item safety bound');
    }
  }
  return items;
}

async function waitForCleanupInventory(currentSandboxId) {
  let result;
  for (let attempt = 0; attempt < 30; attempt += 1) {
    const [finalSandboxes, metadataMatchedSandboxes] = await Promise.all([
      listSandboxes(),
      listSandboxes({
        [RUNTIME_VERIFICATION_RUN_METADATA_KEY]: verificationRunId,
      }),
    ]);
    result = { finalSandboxes, metadataMatchedSandboxes };
    const currentPresentById =
      currentSandboxId &&
      finalSandboxes.some(
        (candidate) => candidate.sandboxId === currentSandboxId,
      );
    if (!currentPresentById && metadataMatchedSandboxes.length === 0) {
      return result;
    }
    await new Promise((resolve) => setTimeout(resolve, 2_000));
  }
  return result;
}

async function run(
  command,
  timeoutMs = 120_000,
  commandRequestTimeoutMs = requestTimeoutMs,
  user,
) {
  const result = await sandbox.commands.run(command, {
    requestTimeoutMs: commandRequestTimeoutMs,
    timeoutMs,
    ...(user === undefined ? {} : { user }),
  });
  if (result.exitCode !== 0) {
    const diagnostics = [result.stdout, result.stderr]
      .map((value) => value.trim())
      .filter(Boolean)
      .join('\n')
      .slice(0, 1_000);
    throw new Error(
      `runtime probe failed with exit code ${result.exitCode}${
        diagnostics ? `: ${diagnostics}` : ''
      }`,
    );
  }
  return result.stdout.trim();
}

async function fetchDesktopSurface(port) {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 30_000);
  try {
    const response = await fetch(`https://${sandbox.getHost(port)}/`, {
      headers: sandbox.trafficAccessToken
        ? { 'e2b-traffic-access-token': sandbox.trafficAccessToken }
        : {},
      signal: controller.signal,
    });
    const body = await response.text();
    if (!response.ok || !body.includes('<div id="app">')) {
      throw new Error(
        `external desktop surface returned ${response.status} or unexpected HTML`,
      );
    }
    return {
      port,
      status: response.status,
      content_type: response.headers.get('content-type'),
      traffic_token: sandbox.trafficAccessToken ? 'present' : 'not-required',
    };
  } finally {
    clearTimeout(timeout);
  }
}

const browserProbe = Buffer.from(String.raw`
const { execFileSync } = require("node:child_process");
const root = execFileSync("npm", ["root", "-g"], { encoding: "utf8" }).trim();
const { chromium } = require(root + "/playwright");
(async () => {
  const browser = await chromium.launch({
    executablePath: "/usr/bin/chromium",
    args: ["--no-sandbox", "--disable-dev-shm-usage"],
    headless: true,
  });
  const page = await browser.newPage();
  await page.setContent("<title>monad-runtime-ok</title><h1>ready</h1>");
  const result = { title: await page.title(), heading: await page.textContent("h1") };
  await browser.close();
  process.stdout.write(JSON.stringify(result));
})().catch((error) => {
  process.stderr.write(String(error));
  process.exit(1);
});
`).toString('base64');

const desktopProbe = Buffer.from(String.raw`
const { readFileSync } = require("node:fs");
const { execFileSync, spawnSync } = require("node:child_process");
const shell = (command) =>
  execFileSync("bash", ["-lc", command], { encoding: "utf8" }).trim();
const succeeds = (command, args = []) =>
  spawnSync(command, args, { stdio: "ignore" }).status === 0;
const service = (name) => {
  const result = spawnSync("s6-svstat", ["/run/service/" + name], {
    encoding: "utf8",
  });
  return {
    ok: result.status === 0 && result.stdout.startsWith("up "),
    status: result.stdout.trim(),
  };
};
const httpStatus = Number(
  shell("curl -fsS -o /tmp/desktop-http -w '%{http_code}' http://127.0.0.1:6080/"),
);
const httpsStatus = Number(
  shell("curl -fkSs -o /dev/null -w '%{http_code}' https://127.0.0.1:6081/"),
);
const s6Pid = Number(shell("pgrep -o -x s6-svscan"));
const forbiddenPaths = shell(
  "find / -xdev \\( -iname '*kasm*' -o -iname '*novnc*' -o -iname '*tigervnc*' \\) -print 2>/dev/null | head -n 20",
);
process.stdout.write(JSON.stringify({
  implementation: "LinuxServer Webtop Selkies on Ubuntu i3",
  http_status: httpStatus,
  https_status: httpsStatus,
  html_app: readFileSync("/tmp/desktop-http", "utf8").includes('<div id="app">'),
  listeners: shell("ss -ltnH | awk '$4 ~ /:(6080|6081)$/ {print $4}'")
    .split("\n")
    .filter(Boolean),
  processes: {
    xvfb: succeeds("pgrep", ["-x", "Xvfb"]),
    selkies: succeeds("pgrep", ["-f", "/lsiopy/bin/selkies"]),
    nginx: succeeds("pgrep", ["-x", "nginx"]),
  },
  services: {
    monad_agent: service("svc-monad-agent"),
    nginx: service("svc-nginx"),
    xorg: service("svc-xorg"),
    dbus: service("svc-dbus"),
    pulseaudio: service("svc-pulseaudio"),
    selkies: service("svc-selkies"),
    de: service("svc-de"),
    watchdog: service("svc-watchdog"),
    xsettingsd: service("svc-xsettingsd"),
    cron_guard: service("svc-cron"),
    docker_guard: service("svc-docker"),
  },
  docker: {
    outer_e2b_guest_visible: succeeds("pgrep", ["-x", "dockerd"]),
    desktop_namespace_running: succeeds("nsenter", [
      "--target",
      String(s6Pid),
      "--pid",
      "--mount",
      "pgrep",
      "-x",
      "dockerd",
    ]),
    dockerd_absent: !succeeds("nsenter", [
      "--target",
      String(s6Pid),
      "--pid",
      "--mount",
      "pgrep",
      "-x",
      "dockerd",
    ]),
  },
  cron_disabled:
    !succeeds("nsenter", [
      "--target",
      String(s6Pid),
      "--pid",
      "--mount",
      "pgrep",
      "-x",
      "cron",
    ]) &&
    !succeeds("nsenter", [
      "--target",
      String(s6Pid),
      "--pid",
      "--mount",
      "pgrep",
      "-x",
      "crond",
    ]),
  forbidden_paths: forbiddenPaths ? forbiddenPaths.split("\n") : [],
}));
`).toString('base64');

const bootstrapReadinessProbe = Buffer.from(String.raw`
const { execFileSync } = require("node:child_process");
const { lstatSync, readFileSync, readlinkSync } = require("node:fs");
const servicePath = "/run/service/svc-monad-agent";
const socketPath = "/var/run/monad/credential-bootstrap.sock";
const serviceStatus = execFileSync(
  "s6-svstat",
  [servicePath],
  { encoding: "utf8" },
).trim();
const servicePidText = execFileSync(
  "s6-svstat",
  ["-o", "pid", servicePath],
  { encoding: "utf8" },
).trim();
if (!/^[1-9][0-9]*$/.test(servicePidText)) {
  throw new Error("supervised monad-agent PID is not canonical");
}
const servicePid = Number(servicePidText);
const namedPids = execFileSync("pgrep", ["-x", "monad-agent"], {
  encoding: "utf8",
}).trim().split(/\s+/).filter(Boolean);
const processPidText = namedPids.length === 1 ? namedPids[0] : "";
const processPid = /^[1-9][0-9]*$/.test(processPidText) ? Number(processPidText) : null;
const supervisorPids = execFileSync("pgrep", ["-x", "s6-svscan"], {
  encoding: "utf8",
}).trim().split(/\s+/).filter(Boolean);
const supervisorPidText = supervisorPids.length === 1 ? supervisorPids[0] : "";
const supervisorPid = /^[1-9][0-9]*$/.test(supervisorPidText)
  ? Number(supervisorPidText)
  : null;
let processNamespacePids = [];
let supervisorNamespacePids = [];
let processNamespace = "";
let supervisorNamespace = "";
if (processPid !== null) {
  processNamespacePids = (${parseNamespacePidVector.toString()})(readFileSync(
    "/proc/" + processPid + "/status",
    "utf8",
  ));
  processNamespace = readlinkSync("/proc/" + processPid + "/ns/pid");
}
if (supervisorPid !== null) {
  supervisorNamespacePids = (${parseNamespacePidVector.toString()})(readFileSync(
    "/proc/" + supervisorPid + "/status",
    "utf8",
  ));
  supervisorNamespace = readlinkSync("/proc/" + supervisorPid + "/ns/pid");
}
const socket = lstatSync(socketPath);
const finalServiceStatus = execFileSync(
  "s6-svstat",
  [servicePath],
  { encoding: "utf8" },
).trim();
const finalServicePidText = execFileSync(
  "s6-svstat",
  ["-o", "pid", servicePath],
  { encoding: "utf8" },
).trim();
const finalSupervisorPids = execFileSync("pgrep", ["-x", "s6-svscan"], {
  encoding: "utf8",
}).trim().split(/\s+/).filter(Boolean);
const supervisorStable =
  finalSupervisorPids.length === 1 &&
  finalSupervisorPids[0] === supervisorPidText &&
  supervisorPid !== null &&
  readlinkSync("/proc/" + supervisorPid + "/ns/pid") === supervisorNamespace;
process.stdout.write(JSON.stringify({
  service_up: /^up(?: |$)/.test(serviceStatus),
  service_stable:
    finalServicePidText === servicePidText &&
    /^up(?: |$)/.test(finalServiceStatus),
  service_pid: servicePid,
  process_pid: processPid,
  unique_supervisor_process: supervisorPids.length === 1,
  supervisor_stable: supervisorStable,
  service_process_namespace_match:
    processNamespacePids.length > 0 &&
    supervisorNamespacePids.length > 0 &&
    processNamespacePids[0] === processPid &&
    supervisorNamespacePids[0] === supervisorPid &&
    processNamespacePids.length === supervisorNamespacePids.length &&
    processNamespacePids[processNamespacePids.length - 1] === servicePid &&
    processNamespace === supervisorNamespace,
  unique_named_process: namedPids.length === 1,
  socket_is_exact_type: socket.isSocket() && !socket.isSymbolicLink(),
  socket_uid: socket.uid,
  socket_gid: socket.gid,
  socket_mode: (socket.mode & 0o7777).toString(8),
}));
`).toString('base64');

const tenantBoundaryProbe = Buffer.from(String.raw`
const { execFileSync } = require("node:child_process");
const {
  lstatSync, readFileSync, readlinkSync, readdirSync, realpathSync, statSync,
} = require("node:fs");
const { basename } = require("node:path");
const sha256 = (path) =>
  execFileSync("sha256sum", [path], { encoding: "utf8" }).trim().split(/\s+/)[0];
const serviceNames = [
  "nginx", "xorg", "dbus", "pulseaudio", "selkies", "de", "watchdog", "xsettingsd",
];
const status = (value) => readFileSync("/proc/" + value + "/status", "utf8");
const cgroup = (value) => readFileSync("/proc/" + value + "/cgroup", "utf8").trim();
const command = (value) => {
  try {
    return readFileSync("/proc/" + value + "/cmdline", "utf8")
      .split("\0").filter(Boolean).join(" ");
  } catch {
    return "";
  }
};
const argv = (value) => {
  try {
    return readFileSync("/proc/" + value + "/cmdline", "utf8")
      .split("\0").filter(Boolean);
  } catch {
    return [];
  }
};
const executable = (value) => {
  try {
    return realpathSync("/proc/" + value + "/exe");
  } catch {
    return "";
  }
};
const processEnvironment = (value) => {
  try {
    return Object.fromEntries(
      readFileSync("/proc/" + value + "/environ", "utf8")
        .split("\0")
        .filter(Boolean)
        .map((entry) => {
          const separator = entry.indexOf("=");
          return separator > 0
            ? [entry.slice(0, separator), entry.slice(separator + 1)]
            : [entry, ""];
        }),
    );
  } catch {
    return {};
  }
};
const processIds = () => readdirSync("/proc")
  .filter((value) => /^\d+$/.test(value))
  .map(Number);
const namespacePidVector = ${parseNamespacePidVector.toString()};
const selectNamespacePid = ${selectOuterPidForNamespacePid.toString()};
const namespaceProcessEvidence = () => processIds().flatMap((outerPid) => {
    try {
      const namespacePids = namespacePidVector(status(outerPid));
      return namespacePids.length === 0 ? [] : [{
        pid: outerPid,
        namespacePids,
        namespace: readlinkSync("/proc/" + outerPid + "/ns/pid"),
        mountNamespace: readlinkSync("/proc/" + outerPid + "/ns/mnt"),
        cgroup: cgroup(outerPid),
      }];
    } catch {
      return [];
    }
  });
const uniqueNamedPid = (name) => {
  const values = execFileSync("pgrep", ["-x", name], { encoding: "utf8" })
    .trim().split(/\s+/).filter(Boolean);
  if (values.length !== 1 || !/^[1-9][0-9]*$/.test(values[0])) {
    throw new Error("named process identity is not unique and canonical");
  }
  return Number(values[0]);
};
const serviceState = (name) => {
  const path = "/run/service/svc-" + name;
  const serviceStatus = execFileSync("s6-svstat", [path], { encoding: "utf8" }).trim();
  const value = execFileSync(
    "s6-svstat",
    ["-o", "pid", path],
    { encoding: "utf8" },
  ).trim();
  if (!/^up(?: |$)/.test(serviceStatus) || !/^[1-9][0-9]*$/.test(value)) {
    throw new Error("supervised service PID is not canonical");
  }
  return { pid: Number(value), up: true };
};
const parentPid = (value) => {
  try {
    const match = status(value).match(/^PPid:[ \t]+(\d+)$/m);
    return match ? Number(match[1]) : 0;
  } catch {
    return 0;
  }
};
const processTree = (root) => {
  const found = new Set([root]);
  let changed = true;
  while (changed) {
    changed = false;
    for (const candidate of processIds()) {
      if (!found.has(candidate) && found.has(parentPid(candidate))) {
        found.add(candidate);
        changed = true;
      }
    }
  }
  return [...found];
};
const ids = (raw, name) => {
  const match = raw.match(new RegExp("^" + name + ":[ \t]+(.+)$", "m"));
  return match ? match[1].trim().split(/\s+/).map(Number) : [];
};
const groups = (raw) => {
  const match = raw.match(/^Groups:[ \t]*(.*)$/m);
  const payload = match ? match[1].trim() : "";
  return payload ? payload.split(/\s+/).map(Number) : [];
};
const exactIdentity = (raw, expected) => {
  const uids = ids(raw, "Uid");
  const gids = ids(raw, "Gid");
  return uids.length === 4 && uids.every((value) => value === expected.uid) &&
    gids.length === 4 && gids.every((value) => value === expected.gid) &&
    JSON.stringify(groups(raw)) === JSON.stringify(expected.groups);
};
const observedIdentity = (raw) => {
  const uids = ids(raw, "Uid");
  const gids = ids(raw, "Gid");
  return {
    uid: uids.length === 4 && uids.every((value) => value === uids[0]) ? uids[0] : -1,
    gid: gids.length === 4 && gids.every((value) => value === gids[0]) ? gids[0] : -1,
    groups: groups(raw),
  };
};
const processSnapshot = (value) => ({
  pid: value,
  ppid: parentPid(value),
  executable: executable(value),
  argv: argv(value),
  identity: observedIdentity(status(value)),
  environment: processEnvironment(value),
});
const verifyPinnedNginxProcesses = ${verifyPinnedNginxProcesses.toString()};
const verifyPinnedWatchdogProcesses = ${verifyPinnedWatchdogProcesses.toString()};
const bindBoundaryMarker = ${bindTenantBoundaryMarker.toString()};
const classifyBoundaryFilesystemStability = ${classifyTenantBoundaryFilesystemStability.toString()};
const classifyBoundaryProbeIdentity = ${classifyTenantBoundaryProbeIdentity.toString()};
const classifyBoundaryEvidence = ${classifyTenantBoundaryEvidence.toString()};
const classifyBoundaryTopology = ${classifyTenantBoundaryTopology.toString()};
const inspectBoundaryMarkerFilesystem = ${inspectTenantBoundaryMarkerFilesystem.toString()};
const procRootPaths = ${tenantBoundaryProcRootPaths.toString()};
const procRootRuntimePath = ${tenantBoundaryRuntimePath.toString()};
const procRootPathChain = ${tenantBoundaryRuntimePathChain.toString()};
const exactFile = (path, mode) => {
  const value = lstatSync(path);
  return value.isFile() && !value.isSymbolicLink() && value.uid === 0 &&
    value.gid === 0 && (value.mode & 0o7777) === mode && realpathSync(path) === path;
};
const exactProcRootFile = (supervisorPid, runtimePath, mode) => {
  const chain = procRootPathChain(supervisorPid, runtimePath);
  const ancestors = chain.slice(0, -1);
  if (!ancestors.every((path) => {
    const value = lstatSync(path);
    return value.isDirectory() && !value.isSymbolicLink() &&
      value.uid === 0 && value.gid === 0;
  })) return false;
  const value = lstatSync(chain[chain.length - 1]);
  return value.isFile() && !value.isSymbolicLink() && value.uid === 0 &&
    value.gid === 0 && (value.mode & 0o7777) === mode;
};
const exactProcRootDirectory = (supervisorPid, runtimePath, mode) => {
  const chain = procRootPathChain(supervisorPid, runtimePath);
  return chain.every((path, index) => {
    const value = lstatSync(path);
    return value.isDirectory() && !value.isSymbolicLink() &&
      value.uid === 0 && value.gid === 0 &&
      (index !== chain.length - 1 || mode === undefined ||
        (value.mode & 0o7777) === mode);
  });
};
let probeStage = "probe_identity";
try {
const probeIdentity = classifyBoundaryProbeIdentity({
  getuid: () => process.getuid(),
  getgid: () => process.getgid(),
});
if (probeIdentity.probe_ok !== true) {
  process.stdout.write(JSON.stringify(probeIdentity));
  process.exit(0);
}
probeStage = "supervisor";
const daemonPid = uniqueNamedPid("monad-agent");
const supervisorPid = uniqueNamedPid("s6-svscan");
probeStage = "daemon_service_mapping";
const daemonServiceState = serviceState("monad-agent");
const daemonNamespacePids = namespacePidVector(status(daemonPid));
const supervisorNamespacePids = namespacePidVector(status(supervisorPid));
const daemonNamespace = readlinkSync("/proc/" + daemonPid + "/ns/pid");
const supervisorNamespace = readlinkSync("/proc/" + supervisorPid + "/ns/pid");
probeStage = "daemon_supervisor_mount_namespace";
const daemonMountNamespace = readlinkSync("/proc/" + daemonPid + "/ns/mnt");
const supervisorMountNamespace = readlinkSync("/proc/" + supervisorPid + "/ns/mnt");
probeStage = "daemon_supervisor_root_identity";
const daemonRootIdentity = statSync("/proc/" + daemonPid + "/root");
const supervisorRootIdentity = statSync("/proc/" + supervisorPid + "/root");
probeStage = "daemon_supervisor_run_identity";
const daemonRunIdentity = statSync("/proc/" + daemonPid + "/root/run");
const supervisorRunIdentity = statSync("/proc/" + supervisorPid + "/root/run");
const topology = classifyBoundaryTopology({
  daemonPid,
  supervisorPid,
  daemonServicePid: daemonServiceState.pid,
  daemonNamespacePids,
  supervisorNamespacePids,
  daemonNamespace,
  supervisorNamespace,
  daemonMountNamespace,
  supervisorMountNamespace,
  daemonRoot: daemonRootIdentity,
  supervisorRoot: supervisorRootIdentity,
  daemonRun: daemonRunIdentity,
  supervisorRun: supervisorRunIdentity,
});
if (topology.probe_ok !== true) {
  process.stdout.write(JSON.stringify(topology));
  process.exit(0);
}
const attestationPath = "/etc/monad/session-rebind-tenant-boundary.json";
const daemonMarkerPaths = procRootPaths(daemonPid);
const supervisorMarkerPaths = procRootPaths(supervisorPid);
const daemonRuntimeRootPath = (path) => procRootRuntimePath(daemonPid, path);
probeStage = "marker_daemon_proc_root_run_error";
const daemonMarkerFilesystem = inspectBoundaryMarkerFilesystem({
  authority: "daemon",
  lstat: lstatSync,
  procRootRun: daemonMarkerPaths.root + "/run",
  admissionRoot: daemonMarkerPaths.admissionRoot,
  marker: daemonMarkerPaths.marker,
});
if (daemonMarkerFilesystem.probe_ok !== true) {
  process.stdout.write(JSON.stringify(daemonMarkerFilesystem));
  process.exit(0);
}
probeStage = "marker_supervisor_proc_root_run_error";
const supervisorMarkerFilesystem = inspectBoundaryMarkerFilesystem({
  authority: "supervisor",
  lstat: lstatSync,
  procRootRun: supervisorMarkerPaths.root + "/run",
  admissionRoot: supervisorMarkerPaths.admissionRoot,
  marker: supervisorMarkerPaths.marker,
});
if (supervisorMarkerFilesystem.probe_ok !== true) {
  process.stdout.write(JSON.stringify(supervisorMarkerFilesystem));
  process.exit(0);
}
probeStage = "marker_binding";
const marker = readFileSync(daemonMarkerPaths.marker, "utf8");
const supervisorMarker = readFileSync(supervisorMarkerPaths.marker, "utf8");
const markerBinding = bindBoundaryMarker(cgroup(daemonPid), marker);
const supervisorMarkerBinding = bindBoundaryMarker(cgroup(daemonPid), supervisorMarker);
const tenantCgroup = markerBinding.tenantCgroup;
const expectedMembership = markerBinding.expectedMembership;
if (
  supervisorMarker !== marker ||
  supervisorMarkerBinding.tenantCgroup !== tenantCgroup ||
  supervisorMarkerBinding.expectedMembership !== expectedMembership
) {
  throw new Error("tenant boundary marker views differ");
}
probeStage = "marker_target";
if (
  !exactProcRootDirectory(daemonPid, tenantCgroup) ||
  !exactProcRootDirectory(supervisorPid, tenantCgroup)
) {
  throw new Error("tenant cgroup marker target is invalid");
}
probeStage = "service_mapping";
const initialServiceStates = Object.fromEntries(
  serviceNames.map((name) => [name, serviceState(name)]),
);
const serviceNamespacePids = Object.fromEntries(
  serviceNames.map((name) => [name, initialServiceStates[name].pid]),
);
const namespaceProcesses = namespaceProcessEvidence();
const serviceLeaders = Object.fromEntries(
  serviceNames.map((name) => [name, selectNamespacePid({
    innerPid: serviceNamespacePids[name],
    expectedMembership,
    expectedNamespace: supervisorNamespace,
    expectedNamespaceDepth: supervisorNamespacePids.length,
    processes: namespaceProcesses,
  })]),
);
const namespaceProcessByPid = new Map(
  namespaceProcesses.map((process) => [process.pid, process]),
);
const serviceLeaderMountNamespaceMatch = serviceNames.every((name) =>
  namespaceProcessByPid.get(serviceLeaders[name])?.mountNamespace ===
    supervisorMountNamespace,
);
const finalServiceStates = Object.fromEntries(
  serviceNames.map((name) => [name, serviceState(name)]),
);
const mappingServiceStateStable = serviceNames.every((name) =>
  finalServiceStates[name].up === true &&
  finalServiceStates[name].pid === initialServiceStates[name].pid);
if (!mappingServiceStateStable) {
  throw new Error("supervised service state changed during namespace mapping");
}
probeStage = "process_attestation";
const serviceTrees = Object.fromEntries(
  serviceNames.map((name) => [name, processTree(serviceLeaders[name])]),
);
const finalMatchers = {
  nginx: (value) => executable(value) === "/usr/sbin/nginx",
  xorg: (value) => /(^|\/)Xvfb(\s|$)/.test(command(value)),
  dbus: (value) => /(^|\/)dbus-daemon(\s|$)/.test(command(value)),
  pulseaudio: (value) => /(^|\/)pulseaudio(\s|$)/.test(command(value)),
  selkies: (value) => command(value).includes("/lsiopy/bin/selkies"),
  de: (value) => /(^|\/)i3(\s|$)/.test(command(value)),
  watchdog: (value) =>
    executable(value) === "/usr/bin/sleep" &&
    JSON.stringify(argv(value)) === JSON.stringify(["sleep", "infinity"]),
  xsettingsd: (value) => /(^|\/)xsettingsd(\s|$)/.test(command(value)),
};
const finalProcesses = Object.fromEntries(serviceNames.map((name) => [
  name,
  serviceTrees[name].filter(finalMatchers[name]),
]));
const expectedIdentity = { uid: 911, gid: 1001, groups: [100] };
const daemonStatus = status(daemonPid);
const nonRootFinalServices = ["xorg", "dbus", "pulseaudio", "selkies", "de", "xsettingsd"];
const nginxIdentityMatch = verifyPinnedNginxProcesses({
  leaderPid: serviceLeaders.nginx,
  processes: serviceTrees.nginx.map(processSnapshot),
});
const watchdogIdentityMatch = verifyPinnedWatchdogProcesses({
  leaderPid: serviceLeaders.watchdog,
  processes: serviceTrees.watchdog.map(processSnapshot),
});
probeStage = "filesystem_attestation";
const runtimeAttestationPath = daemonRuntimeRootPath(attestationPath);
const daemonExecutablePath = "/proc/" + daemonPid + "/exe";
const runtimeDaemonPath = daemonRuntimeRootPath(
  "/opt/monad/runtime/bin/monad-agent",
);
const runtimeEntrypointPath = daemonRuntimeRootPath(
  "/opt/monad/runtime/bin/monad-entrypoint",
);
const runtimeAdmissionHelperPath = daemonRuntimeRootPath(
  "/opt/monad/runtime/libexec/monad-tenant-admission",
);
const attestation = JSON.parse(readFileSync(runtimeAttestationPath, "utf8"));
const daemonSha256 = sha256(daemonExecutablePath);
const runtimeDaemonSha256 = sha256(runtimeDaemonPath);
const entrypointSha256 = sha256(runtimeEntrypointPath);
const admissionHelperSha256 = sha256(runtimeAdmissionHelperPath);
const attestationFilesExact =
  exactProcRootFile(daemonPid, attestationPath, 0o444) &&
  exactProcRootDirectory(daemonPid, "/opt/monad/runtime/bin", 0o755) &&
  exactProcRootFile(daemonPid, "/opt/monad/runtime/bin/monad-agent", 0o755) &&
  exactProcRootFile(daemonPid, "/opt/monad/runtime/bin/monad-entrypoint", 0o755) &&
  exactProcRootDirectory(daemonPid, "/opt", 0o755) &&
  exactProcRootDirectory(daemonPid, "/opt/monad", 0o755) &&
  exactProcRootDirectory(daemonPid, "/opt/monad/runtime", 0o755) &&
  exactProcRootDirectory(daemonPid, "/opt/monad/runtime/libexec", 0o755) &&
  exactProcRootFile(daemonPid, "/opt/monad/runtime/libexec/monad-tenant-admission", 0o755) &&
  serviceNames.every((name) =>
    exactProcRootFile(supervisorPid, "/usr/local/libexec/monad-webtop-svc-" + name, 0o555) &&
    exactProcRootFile(supervisorPid, "/etc/s6-overlay/s6-rc.d/svc-" + name + "/run", 0o755)) &&
  exactProcRootFile(supervisorPid, "/etc/s6-overlay/s6-rc.d/svc-cron/run", 0o755);
const finalMarker = readFileSync(daemonMarkerPaths.marker, "utf8");
const finalSupervisorMarker = readFileSync(supervisorMarkerPaths.marker, "utf8");
const finalMarkerBinding = bindBoundaryMarker(cgroup(daemonPid), finalMarker);
const finalSupervisorMarkerBinding = bindBoundaryMarker(
  cgroup(daemonPid),
  finalSupervisorMarker,
);
const markerParentDaemonCgroupMatch =
  finalMarkerBinding.tenantCgroup === tenantCgroup &&
  finalMarkerBinding.expectedMembership === expectedMembership &&
  finalSupervisorMarkerBinding.tenantCgroup === tenantCgroup &&
  finalSupervisorMarkerBinding.expectedMembership === expectedMembership;
const markerExact =
  exactProcRootFile(daemonPid, "/run/monad-admission/tenant-cgroup-ready", 0o444) &&
  exactProcRootFile(supervisorPid, "/run/monad-admission/tenant-cgroup-ready", 0o444) &&
  finalMarker === marker &&
  finalSupervisorMarker === supervisorMarker &&
  finalSupervisorMarker === finalMarker &&
  exactProcRootDirectory(daemonPid, "/run/monad-admission", 0o700) &&
  exactProcRootDirectory(supervisorPid, "/run/monad-admission", 0o700) &&
  exactProcRootDirectory(daemonPid, tenantCgroup) &&
  exactProcRootDirectory(supervisorPid, tenantCgroup);
probeStage = "daemon_supervisor_filesystem_stability";
const finalDaemonRootIdentity = statSync("/proc/" + daemonPid + "/root");
const finalSupervisorRootIdentity = statSync("/proc/" + supervisorPid + "/root");
const finalDaemonRunIdentity = statSync("/proc/" + daemonPid + "/root/run");
const finalSupervisorRunIdentity = statSync("/proc/" + supervisorPid + "/root/run");
const filesystemStability = classifyBoundaryFilesystemStability({
  initialDaemonRoot: daemonRootIdentity,
  initialSupervisorRoot: supervisorRootIdentity,
  initialDaemonRun: daemonRunIdentity,
  initialSupervisorRun: supervisorRunIdentity,
  finalDaemonRoot: finalDaemonRootIdentity,
  finalSupervisorRoot: finalSupervisorRootIdentity,
  finalDaemonRun: finalDaemonRunIdentity,
  finalSupervisorRun: finalSupervisorRunIdentity,
});
if (filesystemStability.probe_ok !== true) {
  process.stdout.write(JSON.stringify(filesystemStability));
  process.exit(0);
}
const finalDaemonPid = uniqueNamedPid("monad-agent");
const finalSupervisorPid = uniqueNamedPid("s6-svscan");
const finalDaemonServiceState = serviceState("monad-agent");
const finalBoundaryServiceStates = Object.fromEntries(
  serviceNames.map((name) => [name, serviceState(name)]),
);
const serviceStateStable =
  mappingServiceStateStable &&
  serviceNames.every((name) =>
    finalBoundaryServiceStates[name].up === true &&
    finalBoundaryServiceStates[name].pid === initialServiceStates[name].pid);
const daemonStateStable =
  finalDaemonPid === daemonPid &&
  finalDaemonServiceState.up === true &&
  finalDaemonServiceState.pid === daemonServiceState.pid &&
  JSON.stringify(namespacePidVector(status(finalDaemonPid))) ===
    JSON.stringify(daemonNamespacePids) &&
  readlinkSync("/proc/" + finalDaemonPid + "/ns/pid") === daemonNamespace &&
  readlinkSync("/proc/" + finalDaemonPid + "/ns/mnt") === daemonMountNamespace;
const supervisorStateStable =
  finalSupervisorPid === supervisorPid &&
  JSON.stringify(namespacePidVector(status(finalSupervisorPid))) ===
    JSON.stringify(supervisorNamespacePids) &&
  readlinkSync("/proc/" + finalSupervisorPid + "/ns/pid") === supervisorNamespace &&
  readlinkSync("/proc/" + finalSupervisorPid + "/ns/mnt") === supervisorMountNamespace;
const finalServiceLeaderMountNamespaceMatch = serviceNames.every((name) =>
  readlinkSync("/proc/" + serviceLeaders[name] + "/ns/mnt") ===
    supervisorMountNamespace,
);
const boundaryEvidence = {
  daemon_sha256: daemonSha256,
  entrypoint_sha256: entrypointSha256,
  admission_helper_sha256: admissionHelperSha256,
  daemon_service_mapping: true,
  daemon_supervisor_mount_namespace_match: true,
  daemon_supervisor_root_identity_match: true,
  daemon_supervisor_run_identity_match: true,
  daemon_supervisor_filesystem_stable: true,
  service_leader_mount_namespace_match:
    serviceLeaderMountNamespaceMatch && finalServiceLeaderMountNamespaceMatch,
  daemon_executable_match:
    readlinkSync(daemonExecutablePath) === "/opt/monad/runtime/bin/monad-agent" &&
    runtimeDaemonSha256 === daemonSha256,
  attestation_hash_match:
    attestation.daemon?.sha256 === daemonSha256 &&
    attestation.admission_helper?.sha256 === admissionHelperSha256,
  attestation_identity_match:
    attestation.tenant?.uid === expectedIdentity.uid &&
    attestation.tenant?.gid === expectedIdentity.gid &&
    JSON.stringify(attestation.tenant?.groups) === JSON.stringify(expectedIdentity.groups) &&
    JSON.stringify(attestation.tenant?.services) === JSON.stringify({
      chromium: expectedIdentity.uid,
      git: expectedIdentity.uid,
      opencode: expectedIdentity.uid,
      selkies: expectedIdentity.uid,
      xorg: expectedIdentity.uid,
    }),
  attestation_files_exact: attestationFilesExact,
  marker_exact: markerExact,
  marker_basename_match: basename(tenantCgroup) === "monad-tenant",
  marker_parent_daemon_cgroup_match: markerParentDaemonCgroupMatch,
  root_daemon_outside_tenant_cgroup:
    exactIdentity(daemonStatus, { uid: 0, gid: 0, groups: [0] }) &&
    cgroup(daemonPid) !== expectedMembership,
  tenant_service_identity_match:
    nonRootFinalServices.every((name) =>
      finalProcesses[name].length > 0 &&
      finalProcesses[name].every((value) => exactIdentity(status(value), expectedIdentity))),
  nginx_identity_match: nginxIdentityMatch,
  watchdog_identity_match: watchdogIdentityMatch,
  tenant_service_cgroup_match:
    serviceNames.every((name) =>
      serviceTrees[name].every((value) => cgroup(value) === expectedMembership)),
  service_leader_cgroup_match:
    serviceNames.every((name) => cgroup(serviceLeaders[name]) === expectedMembership),
  important_descendant_cgroup_match:
    serviceNames.every((name) =>
      finalProcesses[name].length > 0 &&
      finalProcesses[name].every((value) => cgroup(value) === expectedMembership)),
  service_leaders: serviceLeaders,
  service_namespace_pids: serviceNamespacePids,
  daemon_pid: daemonPid,
  daemon_namespace: daemonNamespace,
  daemon_mount_namespace: daemonMountNamespace,
  daemon_state_stable: daemonStateStable,
  supervisor_pid: supervisorPid,
  supervisor_namespace: supervisorNamespace,
  supervisor_mount_namespace: supervisorMountNamespace,
  supervisor_state_stable: supervisorStateStable,
  service_state_stable: serviceStateStable,
  final_processes: finalProcesses,
};
process.stdout.write(JSON.stringify(classifyBoundaryEvidence(boundaryEvidence)));
} catch {
  process.stdout.write(JSON.stringify({ probe_ok: false, stage: probeStage }));
}
`).toString('base64');

let verificationError;
try {
  const initial = await listSandboxes();
  baseline = summarizeSandboxInventory(initial);
  evidence.baseline = baseline;
  assertSafeVerificationBaseline(baseline);

  sandbox = await Sandbox.create(templateRef, {
    ...connection,
    timeoutMs: 15 * 60 * 1000,
    allowInternetAccess: false,
    network: {
      denyOut: ['0.0.0.0/0'],
      allowPublicTraffic: false,
    },
    lifecycle: { onTimeout: 'pause', autoResume: false },
    metadata: {
      [RUNTIME_VERIFICATION_METADATA_KEY]: manifest.runtime_version,
      [RUNTIME_VERIFICATION_RUN_METADATA_KEY]: verificationRunId,
      [SYNTHETIC_METADATA_KEY]: 'true',
    },
  });
  evidence.sandbox_id = sandbox.sandboxId;

  // The daemon boots credential-gated and cannot reach /monad/health under
  // this verification's deny-all egress (lease activation must mint against
  // the control plane). The verifiable build-time contract is: the daemon
  // process is supervised and up, and it is awaiting the private bootstrap
  // on its root-only socket. Full runtime health belongs to the session path
  // and credentialed canaries.
  const bootstrapGate = JSON.parse(
    await run(`printf '%s' '${bootstrapReadinessProbe}' | base64 -d | node`),
  );
  if (
    bootstrapGate.service_up !== true ||
    bootstrapGate.service_stable !== true ||
    !Number.isSafeInteger(bootstrapGate.service_pid) ||
    bootstrapGate.service_pid <= 1 ||
    !Number.isSafeInteger(bootstrapGate.process_pid) ||
    bootstrapGate.process_pid <= 1 ||
    bootstrapGate.service_process_namespace_match !== true ||
    bootstrapGate.unique_supervisor_process !== true ||
    bootstrapGate.supervisor_stable !== true ||
    bootstrapGate.unique_named_process !== true ||
    bootstrapGate.socket_is_exact_type !== true ||
    bootstrapGate.socket_uid !== 0 ||
    bootstrapGate.socket_gid !== 0 ||
    bootstrapGate.socket_mode !== '600'
  ) {
    throw new Error(
      `daemon bootstrap readiness contract failed: ${JSON.stringify(bootstrapGate)}`,
    );
  }
  evidence.health = {
    daemon: 'supervised',
    daemon_namespace_pid: bootstrapGate.service_pid,
    daemon_pid: bootstrapGate.process_pid,
    credential_bootstrap: 'awaiting',
    bootstrap_socket_uid: bootstrapGate.socket_uid,
    bootstrap_socket_gid: bootstrapGate.socket_gid,
    bootstrap_socket_mode: bootstrapGate.socket_mode,
  };

  const tenantBoundary = await waitForTenantBoundaryEvidence({
    probe: async ({ remainingMs }) => {
      const attemptTimeoutMs = Math.min(30_000, remainingMs);
      return JSON.parse(await run(
        `printf '%s' '${tenantBoundaryProbe}' | base64 -d | node`,
        attemptTimeoutMs,
        attemptTimeoutMs,
        'root',
      ));
    },
    timeoutMs: 180_000,
    intervalMs: 3_000,
  });
  if (
    tenantBoundary.daemon_sha256 !== manifest.daemon_sha256 ||
    tenantBoundary.entrypoint_sha256 !== manifest.entrypoint_sha256 ||
    tenantBoundary.admission_helper_sha256 !==
      manifest.tenant_admission_helper_sha256 ||
    tenantBoundary.daemon_service_mapping !== true ||
    tenantBoundary.daemon_supervisor_mount_namespace_match !== true ||
    tenantBoundary.daemon_supervisor_root_identity_match !== true ||
    tenantBoundary.daemon_supervisor_run_identity_match !== true ||
    tenantBoundary.daemon_supervisor_filesystem_stable !== true ||
    tenantBoundary.service_leader_mount_namespace_match !== true ||
    tenantBoundary.daemon_state_stable !== true ||
    tenantBoundary.attestation_hash_match !== true ||
    tenantBoundary.attestation_identity_match !== true ||
    tenantBoundary.attestation_files_exact !== true ||
    tenantBoundary.marker_exact !== true ||
    tenantBoundary.marker_basename_match !== true ||
    tenantBoundary.marker_parent_daemon_cgroup_match !== true ||
    tenantBoundary.root_daemon_outside_tenant_cgroup !== true ||
    tenantBoundary.tenant_service_identity_match !== true ||
    tenantBoundary.nginx_identity_match !== true ||
    tenantBoundary.watchdog_identity_match !== true ||
    tenantBoundary.tenant_service_cgroup_match !== true ||
    tenantBoundary.service_leader_cgroup_match !== true ||
    tenantBoundary.important_descendant_cgroup_match !== true ||
    tenantBoundary.service_state_stable !== true ||
    tenantBoundary.supervisor_state_stable !== true
  ) {
    throw new Error('runtime tenant-boundary attestation did not verify');
  }
  evidence.tenant_boundary = tenantBoundary;

  evidence.versions = JSON.parse(
    await run(
      String.raw`node -e 'const {execFileSync}=require("node:child_process"); const commands={opencode:["opencode","--version"],agent_browser:["agent-browser","--version"],playwright:["playwright","--version"],git:["git","--version"],monad:["monad","--version"]}; const out={}; for(const [name,args] of Object.entries(commands)){out[name]=execFileSync(args[0],args.slice(1),{encoding:"utf8"}).trim()} process.stdout.write(JSON.stringify(out))'`,
    ),
  );
  if (
    evidence.versions.opencode !== '1.14.28' ||
    evidence.versions.agent_browser !== 'agent-browser 0.27.0' ||
    evidence.versions.playwright !== 'Version 1.60.0'
  ) {
    throw new Error('runtime tool versions do not match their pins');
  }

  const provenance = JSON.parse(
    await run('cat /opt/monad/runtime-provenance.json'),
  );
  evidence.provenance = provenance;
  if (
    provenance.runtime_version !== manifest.runtime_version ||
    provenance.tams_revision !== manifest.tams_revision ||
    provenance.tams_apps_sandbox_tree_oid !==
      manifest.tams_apps_sandbox_tree_oid ||
    provenance.daemon_sha256 !== manifest.daemon_sha256 ||
    provenance.entrypoint_sha256 !== manifest.entrypoint_sha256 ||
    provenance.tenant_admission_helper_sha256 !==
      manifest.tenant_admission_helper_sha256
  ) {
    throw new Error('runtime provenance does not match prepared assets');
  }

  evidence.browser = JSON.parse(
    await run(`printf '%s' '${browserProbe}' | base64 -d | node`),
  );
  if (
    evidence.browser.title !== 'monad-runtime-ok' ||
    evidence.browser.heading !== 'ready'
  ) {
    throw new Error('headless Chromium returned unexpected content');
  }

  const desktop = JSON.parse(
    await run(`printf '%s' '${desktopProbe}' | base64 -d | node`),
  );
  if (
    desktop.http_status !== 200 ||
    desktop.https_status !== 200 ||
    desktop.html_app !== true ||
    desktop.listeners.length < 2 ||
    Object.values(desktop.processes).some((ready) => ready !== true) ||
    Object.values(desktop.services).some((service) => service.ok !== true) ||
    desktop.docker.desktop_namespace_running !== false ||
    desktop.docker.dockerd_absent !== true ||
    desktop.cron_disabled !== true ||
    desktop.forbidden_paths.length !== 0
  ) {
    throw new Error(`desktop runtime contract failed: ${JSON.stringify(desktop)}`);
  }
  const externalDesktop = await fetchDesktopSurface(6080);
  evidence.desktop = {
    ...desktop,
    external: externalDesktop,
  };
  evidence.disk = JSON.parse(
    await run(
      "probe=/workspace/.monad-runtime-disk-probe; dd if=/dev/zero of=\"$probe\" bs=1M count=256 conv=fsync status=none; bytes=$(stat -c %s \"$probe\"); rm -f \"$probe\"; sync; df -m --output=size,used,avail / | tail -n1 | awk -v bytes=\"$bytes\" '{printf \"{\\\"size_mb\\\":%s,\\\"used_mb\\\":%s,\\\"available_mb\\\":%s,\\\"write_probe_bytes\\\":%s}\",$1,$2,$3,bytes}'",
    ),
  );
} catch (error) {
  verificationError = error;
} finally {
  const killedSandboxIds = [];
  if (sandbox) {
    try {
      await sandbox.kill(connection);
      killedSandboxIds.push(sandbox.sandboxId);
    } catch (error) {
      verificationError ??= error;
    }
  }
  try {
    const { finalSandboxes, metadataMatchedSandboxes } =
      await waitForCleanupInventory(sandbox?.sandboxId);
    evidence.cleanup = buildCleanupEvidence({
      baseline: baseline ?? summarizeSandboxInventory([]),
      finalSandboxes,
      metadataMatchedSandboxes,
      currentSandboxId: sandbox?.sandboxId,
      verificationRunId,
      killedSandboxIds,
    });
    if (!evidence.cleanup.zero_leak_verified) {
      verificationError ??= new Error(
        'current runtime-template-verification sandbox remains after cleanup',
      );
    }
  } catch (error) {
    verificationError ??= error;
  }
}

evidence.finished_at = new Date().toISOString();
process.stdout.write(`${JSON.stringify(evidence, null, 2)}\n`);
if (verificationError) {
  process.stderr.write(
    `Monad runtime verification failed: ${safeErrorMessage(verificationError, [apiKey])}\n`,
  );
  process.exitCode = 1;
}
