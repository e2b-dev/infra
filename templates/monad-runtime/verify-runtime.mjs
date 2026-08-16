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

async function run(command, timeoutMs = 120_000) {
  const result = await sandbox.commands.run(command, {
    requestTimeoutMs,
    timeoutMs,
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
const { lstatSync, readFileSync } = require("node:fs");
const servicePath = "/run/service/svc-monad-agent";
const socketPath = "/var/run/monad/credential-bootstrap.sock";
const servicePidText = execFileSync(
  "s6-svstat",
  ["-o", "pid", servicePath],
  { encoding: "utf8" },
).trim();
if (!/^[1-9][0-9]*$/.test(servicePidText)) {
  throw new Error("supervised monad-agent PID is not canonical");
}
const servicePid = Number(servicePidText);
const serviceComm = readFileSync("/proc/" + servicePid + "/comm", "utf8").trim();
const namedPids = execFileSync("pgrep", ["-x", "monad-agent"], {
  encoding: "utf8",
}).trim().split(/\s+/).filter(Boolean);
const socket = lstatSync(socketPath);
process.stdout.write(JSON.stringify({
  service_pid: servicePid,
  service_comm: serviceComm,
  unique_named_process: namedPids.length === 1 && namedPids[0] === servicePidText,
  socket_is_exact_type: socket.isSocket() && !socket.isSymbolicLink(),
  socket_uid: socket.uid,
  socket_gid: socket.gid,
  socket_mode: (socket.mode & 0o7777).toString(8),
}));
`).toString('base64');

const tenantBoundaryProbe = Buffer.from(String.raw`
const { execFileSync } = require("node:child_process");
const { lstatSync, readFileSync, readdirSync, realpathSync } = require("node:fs");
const { basename, dirname } = require("node:path");
const sha256 = (path) =>
  execFileSync("sha256sum", [path], { encoding: "utf8" }).trim().split(/\s+/)[0];
const pid = (args) => Number(execFileSync("pgrep", args, { encoding: "utf8" }).trim());
const serviceNames = [
  "nginx", "xorg", "dbus", "pulseaudio", "selkies", "de", "watchdog", "xsettingsd",
];
const serviceLeaderPid = (name) => Number(execFileSync(
  "s6-svstat",
  ["-o", "pid", "/run/service/svc-" + name],
  { encoding: "utf8" },
).trim());
const status = (value) => readFileSync("/proc/" + value + "/status", "utf8");
const cgroup = (value) => readFileSync("/proc/" + value + "/cgroup", "utf8").trim();
const command = (value) => {
  try {
    return readFileSync("/proc/" + value + "/cmdline", "utf8")
      .split("\\0").filter(Boolean).join(" ");
  } catch {
    return "";
  }
};
const argv = (value) => {
  try {
    return readFileSync("/proc/" + value + "/cmdline", "utf8")
      .split("\\0").filter(Boolean);
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
        .split("\\0")
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
  .filter((value) => /^\\d+$/.test(value))
  .map(Number);
const parentPid = (value) => {
  try {
    const match = status(value).match(/^PPid:[ \\t]+(\\d+)$/m);
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
  const match = raw.match(new RegExp("^" + name + ":[ \\t]+(.+)$", "m"));
  return match ? match[1].trim().split(/\s+/).map(Number) : [];
};
const groups = (raw) => {
  const match = raw.match(/^Groups:[ \\t]*(.*)$/m);
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
const exactFile = (path, mode) => {
  const value = lstatSync(path);
  return value.isFile() && !value.isSymbolicLink() && value.uid === 0 &&
    value.gid === 0 && (value.mode & 0o7777) === mode && realpathSync(path) === path;
};
const markerPath = "/run/monad-admission/tenant-cgroup-ready";
const attestationPath = "/etc/monad/session-rebind-tenant-boundary.json";
const marker = readFileSync(markerPath, "utf8");
if (!marker.endsWith("\n") || marker.slice(0, -1).includes("\n")) {
  throw new Error("tenant cgroup marker is not canonical");
}
const tenantCgroup = marker.slice(0, -1);
if (realpathSync(tenantCgroup) !== tenantCgroup ||
    !tenantCgroup.startsWith("/sys/fs/cgroup/")) {
  throw new Error("tenant cgroup marker escaped the cgroup mount");
}
const expectedMembership = "0::" + tenantCgroup.slice("/sys/fs/cgroup".length);
const daemonPid = pid(["-o", "-x", "monad-agent"]);
const serviceLeaders = Object.fromEntries(
  serviceNames.map((name) => [name, serviceLeaderPid(name)]),
);
const serviceTrees = Object.fromEntries(
  serviceNames.map((name) => [name, processTree(serviceLeaders[name])]),
);
const finalMatchers = {
  nginx: (value) => executable(value) === "/usr/sbin/nginx",
  xorg: (value) => /(^|\\/)Xvfb(\\s|$)/.test(command(value)),
  dbus: (value) => /(^|\\/)dbus-daemon(\\s|$)/.test(command(value)),
  pulseaudio: (value) => /(^|\\/)pulseaudio(\\s|$)/.test(command(value)),
  selkies: (value) => command(value).includes("/lsiopy/bin/selkies"),
  de: (value) => /(^|\\/)i3(\\s|$)/.test(command(value)),
  watchdog: (value) =>
    executable(value) === "/usr/bin/sleep" &&
    JSON.stringify(argv(value)) === JSON.stringify(["sleep", "infinity"]),
  xsettingsd: (value) => /(^|\\/)xsettingsd(\\s|$)/.test(command(value)),
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
const attestation = JSON.parse(readFileSync(attestationPath, "utf8"));
const daemonSha256 = sha256("/usr/local/bin/monad-agent");
const admissionHelperSha256 = sha256("/usr/local/libexec/monad-tenant-admission");
process.stdout.write(JSON.stringify({
  daemon_sha256: daemonSha256,
  admission_helper_sha256: admissionHelperSha256,
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
  attestation_files_exact:
    exactFile(attestationPath, 0o444) &&
    exactFile("/usr/local/bin/monad-agent", 0o755) &&
    exactFile("/usr/local/libexec/monad-tenant-admission", 0o755) &&
    serviceNames.every((name) =>
      exactFile("/usr/local/libexec/monad-webtop-svc-" + name, 0o555) &&
      exactFile("/etc/s6-overlay/s6-rc.d/svc-" + name + "/run", 0o755)) &&
    exactFile("/etc/s6-overlay/s6-rc.d/svc-cron/run", 0o755),
  marker_exact:
    exactFile(markerPath, 0o444) &&
    realpathSync("/run/monad-admission") === "/run/monad-admission" &&
    (lstatSync("/run/monad-admission").mode & 0o7777) === 0o700,
  marker_basename_match: basename(tenantCgroup) === "monad-tenant",
  marker_direct_parent_match: dirname(tenantCgroup) === "/sys/fs/cgroup",
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
  final_processes: finalProcesses,
}));
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
    !Number.isSafeInteger(bootstrapGate.service_pid) ||
    bootstrapGate.service_pid <= 1 ||
    bootstrapGate.service_comm !== 'monad-agent' ||
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
    daemon_pid: bootstrapGate.service_pid,
    credential_bootstrap: 'awaiting',
    bootstrap_socket_uid: bootstrapGate.socket_uid,
    bootstrap_socket_gid: bootstrapGate.socket_gid,
    bootstrap_socket_mode: bootstrapGate.socket_mode,
  };

  const tenantBoundary = JSON.parse(
    await run(`printf '%s' '${tenantBoundaryProbe}' | base64 -d | node`),
  );
  if (
    tenantBoundary.daemon_sha256 !== manifest.daemon_sha256 ||
    tenantBoundary.admission_helper_sha256 !==
      manifest.tenant_admission_helper_sha256 ||
    tenantBoundary.attestation_hash_match !== true ||
    tenantBoundary.attestation_identity_match !== true ||
    tenantBoundary.attestation_files_exact !== true ||
    tenantBoundary.marker_exact !== true ||
    tenantBoundary.marker_basename_match !== true ||
    tenantBoundary.marker_direct_parent_match !== true ||
    tenantBoundary.root_daemon_outside_tenant_cgroup !== true ||
    tenantBoundary.tenant_service_identity_match !== true ||
    tenantBoundary.nginx_identity_match !== true ||
    tenantBoundary.watchdog_identity_match !== true ||
    tenantBoundary.tenant_service_cgroup_match !== true ||
    tenantBoundary.service_leader_cgroup_match !== true ||
    tenantBoundary.important_descendant_cgroup_match !== true
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
      manifest.tams_apps_sandbox_tree_oid
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
