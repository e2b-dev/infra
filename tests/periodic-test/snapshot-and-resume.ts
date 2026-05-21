import { Sandbox } from "@e2b/code-interpreter";
import { DEBUG_TIMEOUT_MS, log, runTestWithSandbox } from "./utils.ts";

const sbx = await Sandbox.create({
  timeoutMs: DEBUG_TIMEOUT_MS,
  lifecycle: { autoResume: true, onTimeout: "pause" },
});
log("ℹ️ sandbox created", sbx.sandboxId);

await runTestWithSandbox(sbx, "snapshot-and-resume", async () => {
  await sbx.runCode("x = 1", {
    requestTimeoutMs: 10000,
  });
  log("Sandbox code executed");

  await sbx.pause();
  log("Sandbox paused");

  // Resume the sandbox from the same state
  let sameSbx = await Sandbox.connect(sbx.sandboxId);
  log("Sandbox resumed", sameSbx.sandboxId);

  let execution = await sameSbx.runCode("x+=1; x", {
    requestTimeoutMs: 10000,
  });
  // Output result
  log("RunCode Output:", execution.text);

  if (execution.text !== "2") {
    log("Test failed:", "The expected runCode output doesn't match");
    throw new Error("Failed to runCode in resumed sandbox");
  }
  log("Sandbox resumed successfully");

  await sbx.pause();
  log("Sandbox paused");

  execution = await sameSbx.runCode("x+=1; x", {
    requestTimeoutMs: 10000,
  });
  // Output result
  log("RunCode Output:", execution.text);

  if (execution.text !== "3") {
    log("Test failed:", "The expected runCode output doesn't match");
    throw new Error("Failed to runCode in auto resumed sandbox");
  }

  log("Sandbox auto resumed successfully");
});
