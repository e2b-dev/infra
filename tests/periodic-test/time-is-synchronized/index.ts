import { Sandbox } from "@e2b/code-interpreter";
import { log, runTestWithSandbox } from "../utils.ts";

// Helper function to stream command output
async function streamCommandOutput(command: string, args: string[]) {
  const cmd = new Deno.Command(command, {
    args,
    stdout: "piped",
    stderr: "piped",
  });

  const process = cmd.spawn();
  const decoder = new TextDecoder();

  let output = "";

  const readStream = async (
    stream: ReadableStream<Uint8Array>,
    logFn: (msg: Uint8Array) => void
  ) => {
    for await (const chunk of stream) {
      logFn(chunk);
      output += decoder.decode(chunk);
    }
  };

  // Run both readers concurrently
  await Promise.all([
    readStream(process.stdout, (chunk) => {
      Deno.stdout.write(chunk);
    }),
    readStream(process.stderr, (chunk) => {
      Deno.stderr.write(chunk);
    }),
  ]);

  // Wait for the process to complete and get the status
  const status = await process.status;
  return { status, output };
}

async function deleteTemplate(templateID: string) {
  const output = await streamCommandOutput("deno", [
    "run",
    "--allow-all",
    "@e2b/cli",
    "template",
    "delete",
    "-y",
    templateID,
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
const buildStatus = await streamCommandOutput("deno", [
  "run",
  "--allow-all",
  "@e2b/cli",
  "template",
  "build",
  "--name",
  templateName,
  "--cmd",
  "echo 'start cmd debug' && sleep 10 && echo 'done starting command debug'",
]);

if (buildStatus.status.code !== 0) {
  throw new Error(`❌ Build failed with code ${buildStatus.status.code}`);
}

console.log("✅ Template built successfully");

// read template id from e2b.toml
const e2bToml = await Deno.readTextFile("e2b.toml");
const templateID = e2bToml.match(/template_id = "(.*)"/)?.[1];

if (!templateID) {
  throw new Error("❌ Template ID not found in e2b.toml");
}

// sleep for 15 seconds to create a time delta
await new Promise((resolve) => setTimeout(resolve, 15000));

// remove the file to make script idempotent in local testing
await Deno.remove("e2b.toml");

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
