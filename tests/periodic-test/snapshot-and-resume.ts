import { Sandbox } from "@e2b/code-interpreter";
import { log } from "./utils.ts";

const sbx = await Sandbox.create();
log("ℹ️ sandbox created", sbx.sandboxId);

await sbx.runCode("x = 1");
log("Sandbox code executed");

const success = await sbx.betaPause();
log("Sandbox paused", success);

// Resume the sandbox from the same state
const sameSbx = await Sandbox.connect(sbx.sandboxId);
log("Sandbox resumed", sameSbx.sandboxId);

const execution = await sameSbx.runCode("x+=1; x");
// Output result
log("RunCode Output:", execution.text);

if (execution.text !== "2") {
  log("Test failed:", "The expected runCode output doesn't match");
  throw new Error("Failed to runCode in resumed sandbox");
}
log("Sandbox resumed successfully");

await sbx.kill();
log("Sandbox deleted");
