import { Sandbox } from "npm:@e2b/code-interpreter";

console.log('Starting sandbox logs test');

let sandbox: Sandbox | null = null;

try {
    // Create sandbox
    console.log('creating sandbox')
    sandbox = await Sandbox.create();
    console.log('Sandbox created with ID:', sandbox.sandboxId);

    const command = new Deno.Command("npx", {
        args: ["@e2b/cli", "sandbox", "logs", "-f", sandbox.sandboxId],
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
    let stdout = decoder.decode(output.stdout);
    console.log('CLI process completed');

    stdout = stdout.split('\n').filter(line => !line.includes(`Logs for sandbox`)).join('\n');
    stdout = stdout.split('\n').filter(line => !line.includes('Stopped printing logs — sandbox not found')).join('\n');

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
