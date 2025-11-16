import { Sandbox } from "@e2b/code-interpreter";
import { DEBUG_TIMEOUT_MS, log, runTestWithSandbox } from "./utils.ts";

// Create a E2B Code Interpreter with JavaScript kernel
log("creating sandbox");
const sandbox = await Sandbox.create({ timeoutMs: DEBUG_TIMEOUT_MS });
log("ℹ️ sandbox created", sandbox.sandboxId);

await runTestWithSandbox(sandbox, "run-code", async () => {
  // Execute JavaScript cells
  log("running code");
  await sandbox.runCode("x = 1", {
    requestTimeoutMs: 10000,
  });
  log("first code executed");
  const execution = await sandbox.runCode("x+=1; x", {
    requestTimeoutMs: 10000,
  });
  log("second code executed");
  // Output result
  log(execution.text);
});

log("done");
