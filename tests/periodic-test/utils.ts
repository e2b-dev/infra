import { Sandbox } from "@e2b/code-interpreter";

export const DEBUG_TIMEOUT_MS = 30 * 60 * 1000; // 30 minutes

export function log(...msg: unknown[]) {
  console.log(new Date().toISOString(), ...msg);
}

/**
 * Runs a test function with a sandbox, handling errors and cleanup.
 * On failure, sandboxes are kept alive for debugging when E2B_TEST_DEBUG_MODE=true.
 */
export async function runTestWithSandbox(
  sandbox: Sandbox,
  testName: string,
  testFn: () => Promise<void>
): Promise<void> {
  try {
    await testFn();
    await sandbox.kill();
    log(`${testName}: sandbox killed`);
  } catch (error) {
    log(`${testName} failed:`, error);
    if (sandbox) {
      await sandbox.setTimeout(DEBUG_TIMEOUT_MS);
      log(
        `\n🔍 Debug this failed sandbox:\n  e2b sandbox connect ${sandbox.sandboxId}\n`
      );
    }
    throw new Error(`error in ${testName}`, { cause: error });
  }
}
