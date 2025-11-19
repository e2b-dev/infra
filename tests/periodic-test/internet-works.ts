import { Sandbox } from "@e2b/code-interpreter";
import { DEBUG_TIMEOUT_MS, log, runTestWithSandbox } from "./utils.ts";

log("Starting sandbox logs test");

// Create sandbox
log("creating sandbox");
const sandbox = await Sandbox.create({ timeoutMs: DEBUG_TIMEOUT_MS });
log("ℹ️ sandbox created", sandbox.sandboxId);

await runTestWithSandbox(sandbox, "internet-works", async () => {
  const out = await sandbox.commands.run(
    "wget https://www.gstatic.com/generate_204",
    {
      requestTimeoutMs: 10000,
    }
  );
  log("wget output", out.stderr);

  const internetWorking = out.stderr.includes("204 No Content");
  // verify internet is working
  if (!internetWorking) {
    log("Internet is not working");
    throw new Error("Internet is not working");
  }

  log("Test passed successfully");
});
