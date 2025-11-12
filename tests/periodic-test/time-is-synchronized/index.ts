import { Sandbox } from "@e2b/code-interpreter";
import { readFile, rm } from "fs/promises";

import { log, runTestWithSandbox } from "../utils.ts";

const uniqueID = crypto.randomUUID();
const templateName = `test-template-${uniqueID}`;

console.log(`Building template ${templateName}...`);
const buildCmd = Bun.spawn([
  "bunx",
  "-p",
  "@e2b/cli",
  "e2b",
  "template",
  "build",
  "--name",
  templateName,
],
  {
    stderr: 'inherit',
    stdout: 'inherit',
  }
);

if (await buildCmd.exited !== 0) {
  throw new Error(`❌ Build failed with code ${await buildCmd.exited}`);
}

console.log("✅ Template built successfully");

const e2bToml = await readFile("e2b.toml", "utf8");
const templateID = e2bToml.match(/template_id = "(.*)"/)?.[1];

if (!templateID) {
  throw new Error("❌ Template ID not found in e2b.toml");
}

try {
  // sleep for 15 seconds to create a time delta between template and real time, so the sandbox time wouldn't match if it is not synchronized.
  await new Promise((resolve) => setTimeout(resolve, 15000));

  log("ℹ️ creating sandbox");
  const sandbox = await Sandbox.create(templateID, { timeoutMs: 10000 });

  await runTestWithSandbox(sandbox, "time-is-synchronized", async () => {
    log("ℹ️ starting command");
    const localDateStart = new Date();
    const localDateStartUnix = localDateStart.getTime() / 1000;
    const dateStdout = await sandbox.commands.run("date +%s%3N");
    const dateUnix = parseFloat(dateStdout.stdout) / 1000;
    const date = new Date(dateUnix);

    const localDateEnd = new Date();
    const localDateEndUnix = localDateEnd.getTime() / 1000;

    log(localDateStart.toISOString(), "local date - start of request");
    log(date.toISOString(), "sandbox date");
    log(localDateEnd.toISOString(), "local date - end of request");

    // check if the diff between sandbox time and local time is less than 2 second (taking into consideration the request latency)
    if (dateUnix < localDateStartUnix - 2 || dateUnix > localDateEndUnix + 2) {
      throw new Error("❌ Date is not synchronized");
    }

    log("✅ date is synchronized");
  });
} finally {
  await rm("e2b.toml");

  // Delete template
  const deleteCmd = Bun.spawn([
    "bunx",
    "-p",
    "@e2b/cli",
    "e2b",
    "template",
    "delete",
    "-y",
    templateID,
  ], {
    stderr: 'inherit',
    stdout: 'inherit',
  });

  if (await deleteCmd.exited !== 0) {
    throw new Error(`❌ Delete failed with code ${await deleteCmd.exited}`);
  }
}
