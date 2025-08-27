import { Sandbox } from "@e2b/code-interpreter";
import { log } from "./utils.ts";

const sandbox = await Sandbox.create();
log("ℹ️ sandbox created", sandbox.sandboxId);

await sandbox.files.write("/hello.txt", "Hello World");
const result = await sandbox.files.read("/hello.txt");

if (result !== "Hello World") {
  log("Failed to read file");
  throw new Error("Failed to create file");
}

log("File created successfully");
