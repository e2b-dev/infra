export const RUNTIME_VERIFICATION_METADATA_KEY =
  'monad.operator.runtime-template-verification';
export const RUNTIME_VERIFICATION_RUN_METADATA_KEY =
  'monad.operator.runtime-template-verification-run';
export const SYNTHETIC_METADATA_KEY = 'monad.operator.synthetic';

function hasOwnMetadata(sandbox, key) {
  return Object.hasOwn(sandbox.metadata ?? {}, key);
}

export function classifySandbox(
  sandbox,
  { currentSandboxId, verificationRunId } = {},
) {
  const runtimeVerification = hasOwnMetadata(
    sandbox,
    RUNTIME_VERIFICATION_METADATA_KEY,
  );
  const synthetic =
    sandbox.metadata?.[SYNTHETIC_METADATA_KEY] === 'true';
  const currentById =
    typeof currentSandboxId === 'string' &&
    sandbox.sandboxId === currentSandboxId;
  const currentByMetadata =
    typeof verificationRunId === 'string' &&
    sandbox.metadata?.[RUNTIME_VERIFICATION_RUN_METADATA_KEY] ===
      verificationRunId;

  return {
    sandbox_id: sandbox.sandboxId,
    state: sandbox.state,
    runtime_template_verification: runtimeVerification,
    synthetic_runtime_template_verification:
      runtimeVerification && synthetic,
    current_by_id: currentById,
    current_by_metadata: currentByMetadata,
  };
}

export function summarizeSandboxInventory(sandboxes, ownership = {}) {
  const classified = sandboxes
    .map((sandbox) => classifySandbox(sandbox, ownership))
    .sort((left, right) => left.sandbox_id.localeCompare(right.sandbox_id));

  return {
    active_sandboxes: classified.length,
    sandbox_ids: classified.map((sandbox) => sandbox.sandbox_id),
    runtime_template_verification_sandbox_ids: classified
      .filter((sandbox) => sandbox.runtime_template_verification)
      .map((sandbox) => sandbox.sandbox_id),
    synthetic_runtime_template_verification_sandbox_ids: classified
      .filter((sandbox) => sandbox.synthetic_runtime_template_verification)
      .map((sandbox) => sandbox.sandbox_id),
    current_sandbox_ids_by_id: classified
      .filter((sandbox) => sandbox.current_by_id)
      .map((sandbox) => sandbox.sandbox_id),
    current_sandbox_ids_by_metadata: classified
      .filter((sandbox) => sandbox.current_by_metadata)
      .map((sandbox) => sandbox.sandbox_id),
  };
}

export function assertSafeVerificationBaseline(summary) {
  if (
    summary.synthetic_runtime_template_verification_sandbox_ids.length !== 0
  ) {
    throw new Error(
      `operator canary team contains ${summary.synthetic_runtime_template_verification_sandbox_ids.length} pre-existing synthetic runtime-template-verification sandbox(es)`,
    );
  }
}

export function buildCleanupEvidence({
  baseline,
  finalSandboxes,
  metadataMatchedSandboxes,
  currentSandboxId,
  verificationRunId,
  killedSandboxIds = [],
  confirmedAt = new Date().toISOString(),
}) {
  const ownership = { currentSandboxId, verificationRunId };
  const finalInventory = summarizeSandboxInventory(
    finalSandboxes,
    ownership,
  );
  const metadataInventory = summarizeSandboxInventory(
    metadataMatchedSandboxes,
    ownership,
  );
  const createdSandboxPresent = currentSandboxId
    ? finalInventory.current_sandbox_ids_by_id.length !== 0
    : false;
  // This is deliberately the result count of the independently filtered API
  // inventory, not a reclassification of the full inventory. The verifier
  // therefore needs both the created ID to disappear from an unfiltered list
  // and the unique run marker to return no matches.
  const runtimeVerificationMatches = metadataMatchedSandboxes.length;
  const zeroLeakVerified =
    createdSandboxPresent === false && runtimeVerificationMatches === 0;

  return {
    active_sandboxes: finalInventory.active_sandboxes,
    active_sandbox_ids: finalInventory.sandbox_ids,
    baseline_active_sandboxes: baseline.active_sandboxes,
    baseline_sandbox_ids: baseline.sandbox_ids,
    killed_sandbox_ids: [...killedSandboxIds].sort(),
    current_sandbox_id: currentSandboxId ?? null,
    created_sandbox_present: createdSandboxPresent,
    runtime_verification_match_scope: 'current_verification_run',
    runtime_verification_matches: runtimeVerificationMatches,
    zero_leak_verified: zeroLeakVerified,
    current_verification_metadata_match_count: runtimeVerificationMatches,
    current_verification_metadata_sandbox_ids:
      metadataInventory.sandbox_ids,
    runtime_template_verification_sandbox_ids:
      finalInventory.runtime_template_verification_sandbox_ids,
    confirmed_at: confirmedAt,
  };
}
