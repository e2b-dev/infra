import { Sandbox } from "@e2b/code-interpreter";
import { log, runTestWithSandbox } from "./utils.ts";

// Create a E2B Code Interpreter with JavaScript kernel
log("creating sandbox");
const sandbox = await Sandbox.create();
log("ℹ️ sandbox created", sandbox.sandboxId);

await runTestWithSandbox(sandbox, "run-code", async () => {
  // Execute JavaScript cells
  log("running code");
  await sandbox.runCode("x = 1");
  log("first code executed");
  const execution = await sandbox.runCode("x+=1; x");
  log("second code executed");
  // Output result
  log(execution.text);
});

log("done");
