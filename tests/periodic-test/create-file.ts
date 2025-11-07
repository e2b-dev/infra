import { Sandbox } from "@e2b/code-interpreter";
import { log, runTestWithSandbox } from "./utils.ts";

const sandbox = await Sandbox.create();
log("ℹ️ sandbox created", sandbox.sandboxId);

await runTestWithSandbox(sandbox, "create-file", async () => {
  await sandbox.files.write("/hello.txt", "Hello World");
  log("File written");
  const result = await sandbox.files.read("/hello.txt");

  if (result !== "Hello World") {
    log("Failed to read file");
    throw new Error("Failed to create file");
  }

  log("File created successfully");
});
