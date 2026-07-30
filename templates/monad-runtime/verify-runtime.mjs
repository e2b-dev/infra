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
    throw new Error(`runtime probe failed with exit code ${result.exitCode}`);
  }
  return result.stdout.trim();
}

const browserProbe = Buffer.from(String.raw`
const { execFileSync } = require("node:child_process");
const root = execFileSync("npm", ["root", "-g"], { encoding: "utf8" }).trim();
const { chromium } = require(root + "/playwright");
(async () => {
  const browser = await chromium.launch({
    executablePath: "/usr/local/bin/chromium",
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

  const health = JSON.parse(
    await run('curl -fsS http://127.0.0.1:8000/monad/health'),
  );
  if (
    health.daemon !== 'ok' ||
    health.opencode !== 'ok' ||
    health.runtimeReady !== true
  ) {
    throw new Error('daemon runtime health contract is not ready');
  }
  evidence.health = {
    daemon: health.daemon,
    opencode: health.opencode,
    runtime_ready: health.runtimeReady,
    auth: health.auth,
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

  await run(
    String.raw`test -z "$(ss -ltnH | awk '$4 ~ /:(6080|6081)$/ {print}')" && ! dpkg-query -W 2>/dev/null | grep -Eiq '(kasm|selkies|novnc|tigervnc)'`,
  );
  evidence.desktop = {
    expected: false,
    listeners_6080_6081: 0,
    packages: [],
  };
  evidence.disk = JSON.parse(
    await run(
      "df -m --output=size,used,avail / | tail -n1 | awk '{printf \"{\\\"size_mb\\\":%s,\\\"used_mb\\\":%s,\\\"available_mb\\\":%s}\",$1,$2,$3}'",
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
