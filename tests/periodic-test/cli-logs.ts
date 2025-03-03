import { Sandbox } from "npm:@e2b/code-interpreter";

console.log('Starting sandbox logs test');

let sandbox: Sandbox | null = null;
let cliProcess: Promise<string> | null = null;

try {
    // Create sandbox
    console.log('creating sandbox')
    sandbox = await Sandbox.create();
    console.log('Sandbox created with ID:', sandbox.sandboxId);

    let strippedId = sandbox.sandboxId.split('-')[0]
    console.log('strippedId:', strippedId)

    const command = new Deno.Command("npx", {
        args: ["@e2b/cli", "sandbox", "logs", "-f", strippedId],
        stdout: "piped",
        stderr: "piped",
    });

    const child = command.spawn();

    // Start collecting logs in background
    console.log('Started CLI logs collection');

    // Kill the sandbox
    console.log('Killing sandbox');
    await sandbox.kill();

    const output = await child.output();
    const decoder = new TextDecoder();
    // Wait for CLI process to complete and get its output
    const stdout = decoder.decode(output.stdout);
    console.log('CLI process completed');

    // Assert that we got some logs
    if (!stdout.trim()) {
        throw new Error('No logs were collected from the sandbox');
    }

    console.log('Collected logs:', stdout);
    console.log('Test passed successfully');

} catch (error) {
    console.error('Test failed:', error);
    throw error;
} finally {
    if (sandbox) {
        try {
            await sandbox.kill();
        } catch (error) {
            console.error('Error closing sandbox:', error);
        }
    }
} 
