import { Sandbox } from "npm:@e2b/code-interpreter";

import { exec } from "node:child_process";
import { promisify } from "node:util";

const execAsync = promisify(exec);

async function runCliLogsCommand(sandboxId: string): Promise<{ stdout: string, stderr: string }> {
    // Run the CLI logs command with -f flag
    return execAsync(`e2b sandbox logs -f ${sandboxId}`);
}

console.log('Starting sandbox logs test');

let sandbox: Sandbox | null = null;
let cliProcess: Promise<{ stdout: string, stderr: string }> | null = null;

try {
    // Create sandbox
    console.log('creating sandbox')
    sandbox = await Sandbox.create();
    console.log('Sandbox created with ID:', sandbox.sandboxId);

    // Start collecting logs in background
    cliProcess = runCliLogsCommand(sandbox.sandboxId);
    console.log('Started CLI logs collection');

    // Kill the sandbox
    console.log('Killing sandbox');
    await sandbox.close();

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
            await sandbox.close();
        } catch (error) {
            console.error('Error closing sandbox:', error);
        }
    }
} 
