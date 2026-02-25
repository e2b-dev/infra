import { Sandbox } from 'e2b';

// Reads E2B_API_KEY, E2B_API_URL, E2B_SANDBOX_URL from environment.
// Positional args override env vars for backwards compat:
//   node test-sandbox.mjs [VM_IP] [TEMPLATE_ID]

const apiKey = process.env.E2B_API_KEY || "e2b_00000000000000000000000000000000";
const vmIp = process.argv[2] || "";
const apiUrl = vmIp ? `http://${vmIp}:80` : (process.env.E2B_API_URL || "");
const sandboxUrl = vmIp ? `http://${vmIp}:3002` : (process.env.E2B_SANDBOX_URL || "");

if (!apiUrl) {
  console.error("Usage: Set E2B_API_URL or pass VM_IP as first argument.");
  console.error("  node test-sandbox.mjs <VM_IP> [TEMPLATE_ID]");
  console.error("  E2B_API_URL=http://192.168.100.10:80 node test-sandbox.mjs");
  process.exit(1);
}

async function discoverTemplateId() {
  const resp = await fetch(`${apiUrl}/templates`, {
    headers: { "X-API-Key": apiKey },
    signal: AbortSignal.timeout(10000),
  });
  if (!resp.ok) throw new Error(`Failed to list templates: ${resp.status} ${resp.statusText}`);
  const templates = await resp.json();
  if (!Array.isArray(templates) || templates.length === 0) {
    throw new Error("No templates found on this VM");
  }
  const tpl = templates[0];
  return tpl.templateID || tpl.envID || tpl.id;
}

async function main() {
  try {
    let templateId = process.argv[3];
    if (!templateId) {
      console.log("Discovering template ID from API...");
      templateId = await discoverTemplateId();
      console.log(`Found template: ${templateId}`);
    }

    console.log(`Creating sandbox (API: ${apiUrl}, template: ${templateId})...`);
    const sandbox = await Sandbox.create(templateId, {
      apiKey,
      apiUrl,
      sandboxUrl,
      timeoutMs: 60000,
    });

    console.log(`Sandbox created: ${sandbox.sandboxId}`);
    await new Promise(r => setTimeout(r, 2000));

    const result = await sandbox.commands.run("echo 'Hello from E2B sandbox!'", { timeoutMs: 30000 });
    console.log(`stdout: ${result.stdout.trim()}`);
    console.log(`exit code: ${result.exitCode}`);

    if (result.exitCode !== 0) {
      throw new Error(`Command exited with code ${result.exitCode}`);
    }
    if (!result.stdout.includes("Hello from E2B sandbox!")) {
      throw new Error(`Unexpected output: ${result.stdout}`);
    }

    const result2 = await sandbox.commands.run("hostname && uname -r", { timeoutMs: 30000 });
    console.log(`host/kernel: ${result2.stdout.trim()}`);

    await sandbox.kill();
    console.log("SUCCESS: Sandbox test passed!");
    process.exit(0);
  } catch (err) {
    console.error("FAIL:", err.message || err);
    process.exit(1);
  }
}

main();
