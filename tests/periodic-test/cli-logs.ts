import { Sandbox } from "npm:@e2b/code-interpreter";

console.log('Starting sandbox logs test');

let sandbox: Sandbox | null = null;


function getLogs(sandboxId: string): Record<string, any>[] {
    const command = new Deno.Command("npx", {
        args: ["@e2b/cli", "sandbox", "logs", "--format", "json", sandboxId],
        stdout: "piped",
        stderr: "piped",
    });
    const {stdout} = command.outputSync();
    if (stdout.length === 0) {
        return []
    }

    const decoder = new TextDecoder();

    // Wait for CLI process to complete and get its output
    const lines = decoder.decode(stdout).trim().split('\n');

    console.log('Lines:', lines);
    return lines.map((line) => JSON.parse(line));
}

try {
    // Create sandbox
    console.log('creating sandbox')
    sandbox = await Sandbox.create();
    console.log('Sandbox created with ID:', sandbox.sandboxId);

    // Run a command in the sandbox, so we can test both
    await sandbox.commands.run('echo "Hello, World!"');

    // Kill the sandbox
    console.log('Killing sandbox');
    await sandbox.kill();


    // It takes some time for logs to be propagated to our log store
    const timeout = 10_000;
    const start = Date.now();
    let logsFromAPI = false
    let logsFromSandbox = false
    let logs
    let i = 1;

    while (Date.now() - start < timeout && (!logsFromAPI || !logsFromSandbox)) {
        console.log(`[${i}] Checking logs`);
        logs = getLogs(sandbox.sandboxId);
        for (const log of logs) {
            if (log.logger === 'orchestration-api') {
                logsFromAPI = true;
            } else if (log.logger === 'process') {
                logsFromSandbox = true;
            }
        }

        i++;
        await new Promise((resolve) => setTimeout(resolve, 1000));
    }

    if (!logsFromAPI || !logsFromSandbox) {
        console.log('Logs:', logs);
        console.log('Logs from API:', logsFromAPI, 'Logs from sandbox:', logsFromSandbox);
        throw new Error('Logs not collected');
    }
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
