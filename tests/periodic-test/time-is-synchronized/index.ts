import { Sandbox } from "@e2b/code-interpreter";
import { readFile, rm } from "fs/promises";
import { log, runTestWithSandbox } from "../utils.ts";

// Helper function to stream command output
async function streamCommandOutput(command: string, args: string[]) {
  let output = "";
  let exitCode: number | null = null;

  const proc = Bun.spawn([command, ...args], {
    stdout: "pipe",
    stderr: "pipe"
  });

  // Stream output (stdout and stderr)
  const decoder = new TextDecoder();

  const readStream = async (readable: ReadableStream<Uint8Array>, logFn: (chunk: Uint8Array) => void) => {
    const reader = readable.getReader();
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      if (value) {
        logFn(value);
        output += decoder.decode(value);
      }
    }
  };

  // Process stdout/stderr concurrently
  await Promise.all([
    readStream(proc.stdout, (chunk) => Bun.write(Bun.stdout, chunk)),
    readStream(proc.stderr, (chunk) => Bun.write(Bun.stderr, chunk))
  ]);

  exitCode = await proc.exited;

  return {
    status: { code: exitCode },
    output
  };
}

async function deleteTemplate(templateID: string) {
  const output = await streamCommandOutput("bunx", [
    "@e2b/cli",
    "template",
    "delete",
    "-y",
    templateID
  ]);

  if (output.status.code !== 0) {
    throw new Error(`❌ Delete failed with code ${output.status.code}`);
  }
}

const uniqueID = crypto.randomUUID();
const templateName = `test-template-${uniqueID}`;
console.log("ℹ️ templateName:", templateName);

// Build template command with streaming output
console.log(`Building template ${templateName}...`);
const buildStatus = await streamCommandOutput("bunx", [
  "@e2b/cli",
  "template",
  "build",
  "--name",
  templateName,
  "--cmd",
  "echo 'start cmd debug' && sleep 10 && echo 'done starting command debug'"
]);

if (buildStatus.status.code !== 0) {
  throw new Error(`❌ Build failed with code ${buildStatus.status.code}`);
}

console.log("✅ Template built successfully");

// read template id from e2b.toml
const e2bToml = await readFile("e2b.toml", "utf8");
const templateID = e2bToml.match(/template_id = "(.*)"/)?.[1];

if (!templateID) {
  throw new Error("❌ Template ID not found in e2b.toml");
}

// sleep for 15 seconds to create a time delta
await new Promise((resolve) => setTimeout(resolve, 15000));

// remove the file to make script idempotent in local testing
await rm("e2b.toml");

if (!templateID) {
  throw new Error("❌ Template not found");
}

log("ℹ️ creating sandbox");
let sandbox: Sandbox;
try {
  sandbox = await Sandbox.create(templateID, { timeoutMs: 10000 });
  log("ℹ️ sandbox created", sandbox.sandboxId);
} catch (e) {
  await deleteTemplate(templateID);
  throw e;
}

try {
  await runTestWithSandbox(sandbox, "time-is-synchronized", async () => {
    log("ℹ️ starting command");
    const localDateStart = new Date().getTime() / 1000;
    const date = await sandbox.commands.run("date +%s%3N");
    const localDateEnd = new Date().getTime() / 1000;
    const dateUnix = parseFloat(date.stdout) / 1000;

    log("local date - start of request", localDateStart);
    log("local date - end of request", localDateEnd);
    log("sandbox date", dateUnix);

    // check if the diff between sandbox time and local time is less than 1 second (taking into consideration the request latency)
    if (dateUnix < localDateStart - 1 || dateUnix > localDateEnd + 1) {
      throw new Error("❌ Date is not synchronized");
    }

    log("✅ date is synchronized");
  });
} finally {
  // delete template
  await deleteTemplate(templateID);
}
