import { Sandbox } from "@e2b/code-interpreter";
import { DEBUG_TIMEOUT_MS, log, runTestWithSandbox } from "./utils.ts";

const sbx = await Sandbox.create({ timeoutMs: DEBUG_TIMEOUT_MS });
log("ℹ️ sandbox created", sbx.sandboxId);

await runTestWithSandbox(sbx, "snapshot-and-resume", async () => {
  await sbx.runCode("x = 1", {
    requestTimeoutMs: 10000,
  });
  log("Sandbox code executed");

  const success = await sbx.betaPause();
  log("Sandbox paused", success);

  // Resume the sandbox from the same state
  const sameSbx = await Sandbox.connect(sbx.sandboxId);
  log("Sandbox resumed", sameSbx.sandboxId);

  const execution = await sameSbx.runCode("x+=1; x", {
    requestTimeoutMs: 10000,
  });
  // Output result
  log("RunCode Output:", execution.text);

  if (execution.text !== "2") {
    log("Test failed:", "The expected runCode output doesn't match");
    throw new Error("Failed to runCode in resumed sandbox");
  }
  log("Sandbox resumed successfully");
});
