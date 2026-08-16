import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';

export const SOURCE_FILE = new URL('./runtime-source.json', import.meta.url);
export const DOCKERFILE = new URL('./e2b.Dockerfile', import.meta.url);
export const TEMPLATE_DEFINITION = new URL('./template.mjs', import.meta.url);
export const RUNTIME_BUILD_FILES = [
  new URL('./prepare-assets.mjs', import.meta.url),
  new URL('./runtime-core.mjs', import.meta.url),
  new URL('./runtime-process-contract.mjs', import.meta.url),
  new URL('./runtime-preflight.mjs', import.meta.url),
  new URL('./tenant-boundary.mjs', import.meta.url),
];
export const TENANT_SUPERVISED_SERVICES = Object.freeze([
  'nginx',
  'xorg',
  'dbus',
  'pulseaudio',
  'selkies',
  'de',
  'watchdog',
  'xsettingsd',
]);
export const S6_SERVICE_FILES = [
  new URL('./s6-overlay/s6-rc.d/svc-monad-agent/run', import.meta.url),
  new URL('./s6-overlay/s6-rc.d/svc-monad-agent/type', import.meta.url),
  new URL(
    './s6-overlay/s6-rc.d/svc-monad-agent/dependencies.d/init-services',
    import.meta.url,
  ),
  ...TENANT_SUPERVISED_SERVICES.map(
    (service) => new URL(`./s6-overlay/s6-rc.d/svc-${service}/run`, import.meta.url),
  ),
  new URL('./s6-overlay/s6-rc.d/svc-cron/run', import.meta.url),
];

export function requiredEnv(environment, name) {
  const value = environment[name]?.trim();
  if (!value) {
    throw new Error(`${name} is required`);
  }
  return value;
}

export function normalizeApiUrl(value) {
  const url = new URL(value);
  if (url.protocol !== 'https:') {
    throw new Error('E2B_API_URL must use HTTPS');
  }
  if (url.username || url.password || url.search || url.hash) {
    throw new Error(
      'E2B_API_URL must not contain credentials, query, or fragment',
    );
  }
  url.pathname = url.pathname.replace(/\/+$/, '');
  return url.toString().replace(/\/$/, '');
}

export function validateTemplateRef(value) {
  if (!/^monad-runtime:[a-z0-9][a-z0-9.-]*$/.test(value)) {
    throw new Error(
      'E2B_TEMPLATE_REF must be an immutable monad-runtime:<tag> reference',
    );
  }
  if (value.endsWith(':latest')) {
    throw new Error('E2B_TEMPLATE_REF must not use the mutable latest tag');
  }
  return value;
}

export function canonicalRuntimeTemplateRef(runtimeVersion) {
  if (!/^[0-9a-f]{64}$/.test(runtimeVersion)) {
    throw new Error('runtime version must be one canonical SHA-256 digest');
  }
  return `monad-runtime:desktop-${runtimeVersion.slice(0, 12)}`;
}

export function assertCanonicalRuntimeTemplateRef(templateRef, runtimeVersion) {
  const expected = canonicalRuntimeTemplateRef(runtimeVersion);
  if (templateRef !== expected) {
    throw new Error(
      'E2B_TEMPLATE_REF must exactly match runtime version as ' + expected,
    );
  }
  return templateRef;
}

export async function loadRuntimeSource() {
  return JSON.parse(await readFile(SOURCE_FILE, 'utf8'));
}

export async function calculateRuntimeVersion(source) {
  const dockerfile = await readFile(DOCKERFILE);
  const templateDefinition = await readFile(TEMPLATE_DEFINITION);
  const runtimeBuildFiles = await Promise.all(
    RUNTIME_BUILD_FILES.map((file) => readFile(file)),
  );
  const s6ServiceFiles = await Promise.all(
    S6_SERVICE_FILES.map((file) => readFile(file)),
  );
  const versionInput = {
    schema_version: source.schema_version,
    tams_revision: source.tams_revision,
    tams_apps_sandbox_tree_oid: source.tams_apps_sandbox_tree_oid,
    runtime_input_tree_oids: source.runtime_input_tree_oids,
    tool_versions: source.tool_versions,
    template_resources: source.template_resources,
  };
  const hash = createHash('sha256')
    .update(dockerfile)
    .update('\0')
    .update(templateDefinition)
    .update('\0')
    .update(JSON.stringify(versionInput));
  for (const file of s6ServiceFiles) {
    hash.update('\0').update(file);
  }
  for (const file of runtimeBuildFiles) {
    hash.update('\0').update(file);
  }
  return hash.digest('hex');
}

export function safeErrorMessage(error, secrets = []) {
  let message = error instanceof Error ? error.message : String(error);
  for (const secret of secrets) {
    if (secret) {
      message = message.split(secret).join('[redacted]');
    }
  }
  return message;
}
