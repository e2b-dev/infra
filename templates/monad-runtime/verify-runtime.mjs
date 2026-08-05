import { readFile } from 'node:fs/promises';
import { Sandbox } from 'e2b';
import {
  normalizeApiUrl,
  requiredEnv,
  safeErrorMessage,
  validateTemplateRef,
} from './runtime-core.mjs';

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
const evidence = {
  template_ref: templateRef,
  runtime_version: manifest.runtime_version,
  started_at: new Date().toISOString(),
};
let sandbox;

async function listSandboxes() {
  const paginator = Sandbox.list({
    ...connection,
    limit: 100,
    query: { state: ['running', 'paused'] },
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
    selkies: service("svc-selkies"),
    xorg: service("svc-xorg"),
    nginx: service("svc-nginx"),
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
  },
  forbidden_paths: forbiddenPaths ? forbiddenPaths.split("\n") : [],
}));
`).toString('base64');

let verificationError;
try {
  const initial = await listSandboxes();
  if (initial.length !== 0) {
    throw new Error(
      `operator canary team must be empty; found ${initial.length} sandbox(es)`,
    );
  }

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
      'monad.operator.runtime-template-verification': manifest.runtime_version,
      'monad.operator.synthetic': 'true',
    },
  });
  evidence.sandbox_id = sandbox.sandboxId;

  // The daemon boots credential-gated and cannot reach /monad/health under
  // this verification's deny-all egress (lease activation must mint against
  // the control plane). The verifiable build-time contract is: the daemon
  // process is supervised and up, and it is awaiting the private bootstrap
  // on its root-only socket. Full runtime health belongs to the session path
  // and credentialed canaries.
  const bootstrapGate = (
    await run(
      "sh -c 'pgrep -x monad-agent >/dev/null && echo daemon-running || echo daemon-missing; test -S /var/run/monad/credential-bootstrap.sock && echo awaiting-bootstrap || echo socket-missing; stat -c %a /var/run/monad/credential-bootstrap.sock 2>/dev/null || true'",
    )
  ).trim().split('\n');
  if (
    bootstrapGate[0] !== 'daemon-running' ||
    bootstrapGate[1] !== 'awaiting-bootstrap'
  ) {
    throw new Error(
      `daemon is not awaiting credential bootstrap: ${bootstrapGate.join(', ')}`,
    );
  }
  if (bootstrapGate[2] !== '600') {
    throw new Error(
      `credential bootstrap socket mode is ${bootstrapGate[2] ?? 'unknown'}; expected root-only 600`,
    );
  }
  evidence.health = {
    daemon: 'running',
    credential_bootstrap: 'awaiting',
    bootstrap_socket_mode: bootstrapGate[2],
  };

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
  if (sandbox) {
    try {
      await sandbox.kill(connection);
    } catch (error) {
      verificationError ??= error;
    }
  }
  try {
    const remaining = await listSandboxes();
    evidence.cleanup = {
      active_sandboxes: remaining.length,
      confirmed_at: new Date().toISOString(),
    };
    if (remaining.length !== 0) {
      verificationError ??= new Error(
        `${remaining.length} sandbox(es) remain after cleanup`,
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
