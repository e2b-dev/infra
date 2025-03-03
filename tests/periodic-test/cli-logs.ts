import { Sandbox } from "npm:@e2b/code-interpreter";

import { exec } from "node:child_process";
import { promisify } from "node:util";

const execAsync = promisify(exec);

async function runCliLogsCommand(sandboxId: string): Promise<{ stdout: string, stderr: string }> {
    // Run the CLI logs command with -f flag
    return execAsync(`npx @e2b/cli sandbox logs -f ${sandboxId}`);
}

console.log('Starting sandbox logs test');

let sandbox: Sandbox | null = null;
let cliProcess: Promise<{ stdout: string, stderr: string }> | null = null;

try {
    // Create sandbox
    console.log('creating sandbox')
    sandbox = await Sandbox.create();
    console.log('Sandbox created with ID:', sandbox.sandboxId);

    let strippedId = sandbox.sandboxId.split('-')[0]
    console.log('strippedId:', strippedId)

    // Start collecting logs in background
    cliProcess = runCliLogsCommand(strippedId);
    console.log('Started CLI logs collection');

    // Kill the sandbox
    console.log('Killing sandbox');
    await sandbox.kill();

    // Wait for CLI process to complete and get its output
    const { stdout, stderr } = await cliProcess;
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
