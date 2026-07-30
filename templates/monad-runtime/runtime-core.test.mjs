import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import {
  calculateRuntimeVersion,
  loadRuntimeSource,
  normalizeApiUrl,
  validateTemplateRef,
} from './runtime-core.mjs';

test('runtime source pins immutable TAMS and tool inputs', async () => {
  const source = await loadRuntimeSource();
  assert.match(source.tams_revision, /^[0-9a-f]{40}$/);
  assert.match(source.tams_apps_sandbox_tree_oid, /^[0-9a-f]{40}$/);
  assert.equal(source.tool_versions.opencode, '1.14.28');
  assert.equal(source.tool_versions.agent_browser, '0.27.0');
  assert.equal(source.tool_versions.playwright, '1.60.0');
  assert.equal(source.template_resources.cpu_count, 2);
  assert.equal(source.template_resources.memory_mb, 4096);
});

test('PR A Dockerfile has the base runtime and no desktop layer', async () => {
  const dockerfile = await readFile(
    new URL('./e2b.Dockerfile', import.meta.url),
    'utf8',
  );
  assert.match(dockerfile, /opencode-ai@1\.14\.28/);
  assert.match(dockerfile, /agent-browser@0\.27\.0/);
  assert.match(dockerfile, /playwright@1\.60\.0/);
  assert.match(dockerfile, /COPY \.build-assets\/monad-agent/);
  assert.doesNotMatch(dockerfile, /\b(?:kasm|selkies|vnc)\b/i);
});

test('template readiness uses the public daemon health contract', async () => {
  const definition = await readFile(
    new URL('./template.mjs', import.meta.url),
    'utf8',
  );
  assert.match(definition, /8000\/monad\/health/);
  assert.match(definition, /\.runtimeReady == true/);
  assert.match(definition, /runtime-provenance\.json/);
  assert.doesNotMatch(definition, /\.setEnvs\(/);
  assert.doesNotMatch(definition, /8000\/health/);
});

test('runtime version changes with tree identity', async () => {
  const source = await loadRuntimeSource();
  const first = await calculateRuntimeVersion({
    ...source,
    runtime_input_tree_oids: { 'apps/sandbox': 'a'.repeat(40) },
  });
  const second = await calculateRuntimeVersion({
    ...source,
    runtime_input_tree_oids: { 'apps/sandbox': 'b'.repeat(40) },
  });
  assert.match(first, /^[0-9a-f]{64}$/);
  assert.notEqual(first, second);
});

test('template reference is immutable and correctly named', () => {
  assert.equal(
    validateTemplateRef('monad-runtime:base-1234abcd'),
    'monad-runtime:base-1234abcd',
  );
  assert.throws(() => validateTemplateRef('monad-runtime:latest'));
  assert.throws(() => validateTemplateRef('other-runtime:v1'));
});

test('API URL must be credential-free HTTPS', () => {
  assert.equal(
    normalizeApiUrl('https://api.e2b.monad0.net/'),
    'https://api.e2b.monad0.net',
  );
  assert.throws(() => normalizeApiUrl('http://api.e2b.monad0.net'));
  assert.throws(() => normalizeApiUrl('https://user:pass@example.test'));
});
