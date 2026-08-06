import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import {
  assertSafeVerificationBaseline,
  buildCleanupEvidence,
  classifySandbox,
  RUNTIME_VERIFICATION_METADATA_KEY,
  RUNTIME_VERIFICATION_RUN_METADATA_KEY,
  summarizeSandboxInventory,
  SYNTHETIC_METADATA_KEY,
} from './runtime-verification-inventory.mjs';

const sandbox = (sandboxId, metadata = {}, state = 'running') => ({
  sandboxId,
  metadata,
  state,
});

test('classifies real, stale verification, and current verification sandboxes', () => {
  const ownership = {
    currentSandboxId: 'current',
    verificationRunId: 'run-current',
  };

  assert.deepEqual(classifySandbox(sandbox('real'), ownership), {
    sandbox_id: 'real',
    state: 'running',
    runtime_template_verification: false,
    synthetic_runtime_template_verification: false,
    current_by_id: false,
    current_by_metadata: false,
  });
  assert.deepEqual(
    classifySandbox(
      sandbox('stale', {
        [RUNTIME_VERIFICATION_METADATA_KEY]: 'old-runtime',
        [SYNTHETIC_METADATA_KEY]: 'true',
      }),
      ownership,
    ),
    {
      sandbox_id: 'stale',
      state: 'running',
      runtime_template_verification: true,
      synthetic_runtime_template_verification: true,
      current_by_id: false,
      current_by_metadata: false,
    },
  );
  assert.deepEqual(
    classifySandbox(
      sandbox('current', {
        [RUNTIME_VERIFICATION_METADATA_KEY]: 'runtime',
        [RUNTIME_VERIFICATION_RUN_METADATA_KEY]: 'run-current',
        [SYNTHETIC_METADATA_KEY]: 'true',
      }),
      ownership,
    ),
    {
      sandbox_id: 'current',
      state: 'running',
      runtime_template_verification: true,
      synthetic_runtime_template_verification: true,
      current_by_id: true,
      current_by_metadata: true,
    },
  );
});

test('allows concurrent real sessions while retaining their final total', () => {
  const baseline = summarizeSandboxInventory([
    sandbox('real-before', {}, 'paused'),
  ]);
  assert.doesNotThrow(() => assertSafeVerificationBaseline(baseline));

  const cleanup = buildCleanupEvidence({
    baseline,
    finalSandboxes: [
      sandbox('real-before', {}, 'paused'),
      sandbox('real-concurrent'),
    ],
    metadataMatchedSandboxes: [],
    currentSandboxId: 'verification-current',
    verificationRunId: 'run-current',
    killedSandboxIds: ['verification-current'],
    confirmedAt: '2026-08-07T00:00:00.000Z',
  });

  assert.equal(cleanup.active_sandboxes, 2);
  assert.deepEqual(cleanup.active_sandbox_ids, [
    'real-before',
    'real-concurrent',
  ]);
  assert.equal(cleanup.created_sandbox_present, false);
  assert.equal(
    cleanup.runtime_verification_match_scope,
    'current_verification_run',
  );
  assert.equal(cleanup.runtime_verification_matches, 0);
  assert.equal(cleanup.zero_leak_verified, true);
});

test('rejects a stale runtime-template-verification synthetic sandbox', () => {
  const baseline = summarizeSandboxInventory([
    sandbox('stale', {
      [RUNTIME_VERIFICATION_METADATA_KEY]: 'old-runtime',
      [SYNTHETIC_METADATA_KEY]: 'true',
    }),
    sandbox('real'),
  ]);

  assert.deepEqual(
    baseline.synthetic_runtime_template_verification_sandbox_ids,
    ['stale'],
  );
  assert.throws(
    () => assertSafeVerificationBaseline(baseline),
    /1 pre-existing synthetic runtime-template-verification sandbox/,
  );
});

test('permits unrelated synthetic and non-synthetic marker baselines', () => {
  const baseline = summarizeSandboxInventory([
    sandbox('other-canary', {
      'monad.operator.canary': 'lifecycle-run',
      [SYNTHETIC_METADATA_KEY]: 'true',
    }),
    sandbox('non-synthetic-marker', {
      [RUNTIME_VERIFICATION_METADATA_KEY]: 'user-metadata',
    }),
  ]);

  assert.deepEqual(
    baseline.synthetic_runtime_template_verification_sandbox_ids,
    [],
  );
  assert.doesNotThrow(() => assertSafeVerificationBaseline(baseline));
});

test('does not accept count-only cleanup when the current sandbox remains', () => {
  const baseline = summarizeSandboxInventory([sandbox('real')]);
  const current = sandbox('verification-current', {
    [RUNTIME_VERIFICATION_METADATA_KEY]: 'runtime',
    [RUNTIME_VERIFICATION_RUN_METADATA_KEY]: 'run-current',
    [SYNTHETIC_METADATA_KEY]: 'true',
  });

  const presentById = buildCleanupEvidence({
    baseline,
    finalSandboxes: [sandbox('real'), current],
    metadataMatchedSandboxes: [],
    currentSandboxId: 'verification-current',
    verificationRunId: 'run-current',
  });
  assert.equal(presentById.created_sandbox_present, true);
  assert.equal(presentById.zero_leak_verified, false);

  const presentByMetadata = buildCleanupEvidence({
    baseline,
    finalSandboxes: [sandbox('real')],
    metadataMatchedSandboxes: [current],
    currentSandboxId: 'verification-current',
    verificationRunId: 'run-current',
  });
  assert.equal(presentByMetadata.created_sandbox_present, false);
  assert.equal(presentByMetadata.runtime_verification_matches, 1);
  assert.equal(presentByMetadata.zero_leak_verified, false);
});

test('post-kill cleanup fetches full-ID and unique-run inventories separately', async () => {
  const source = await readFile(
    new URL('./verify-runtime.mjs', import.meta.url),
    'utf8',
  );
  const functionSource = source.match(
    /async function waitForCleanupInventory[\s\S]*?\n}\n/,
  )?.[0];

  assert.ok(functionSource, 'cleanup inventory function is present');
  assert.match(
    functionSource,
    /Promise\.all\(\[\s*listSandboxes\(\),\s*listSandboxes\(\{[\s\S]*RUNTIME_VERIFICATION_RUN_METADATA_KEY/,
  );
  assert.match(
    functionSource,
    /candidate\.sandboxId === currentSandboxId/,
  );
  assert.match(functionSource, /metadataMatchedSandboxes\.length === 0/);
});
