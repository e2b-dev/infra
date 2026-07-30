import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';

export const SOURCE_FILE = new URL('./runtime-source.json', import.meta.url);
export const DOCKERFILE = new URL('./e2b.Dockerfile', import.meta.url);
export const TEMPLATE_DEFINITION = new URL('./template.mjs', import.meta.url);

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

export async function loadRuntimeSource() {
  return JSON.parse(await readFile(SOURCE_FILE, 'utf8'));
}

export async function calculateRuntimeVersion(source) {
  const dockerfile = await readFile(DOCKERFILE);
  const templateDefinition = await readFile(TEMPLATE_DEFINITION);
  const versionInput = {
    schema_version: source.schema_version,
    tams_revision: source.tams_revision,
    tams_apps_sandbox_tree_oid: source.tams_apps_sandbox_tree_oid,
    runtime_input_tree_oids: source.runtime_input_tree_oids,
    tool_versions: source.tool_versions,
    template_resources: source.template_resources,
  };
  return createHash('sha256')
    .update(dockerfile)
    .update('\0')
    .update(templateDefinition)
    .update('\0')
    .update(JSON.stringify(versionInput))
    .digest('hex');
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
