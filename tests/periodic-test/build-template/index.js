import { Sandbox } from '@e2b/code-interpreter';
import { spawn } from 'child_process';
import { randomUUID } from 'crypto';
import fs from 'fs/promises';

// Helper function to stream command output
async function streamCommandOutput(command, args, env) {
    const process = spawn(
        command,
        args,
        {
            env: env
        }
    );
    let output = '';

    // Stream stdout
    process.stdout.on('data', (data) => {
        console.log(data.toString());
        output += data.toString();
    });

    // Stream stderr
    process.stderr.on('data', (data) => {
        console.error(data.toString());
        output += data.toString();
    });

    // Wait for the process to complete and get the status
    return new Promise((resolve, reject) => {
        process.on('close', (code) => {
            resolve({
                status: { code },
                output
            });
        });
        process.on('error', reject);
    });
}

const uniqueID = randomUUID();
const templateName = `test-template-${uniqueID}`;
console.log('templateName:', templateName);

// print all env variables
const envVars = await streamCommandOutput('printenv', [], process.env);
console.log('envVars:', envVars);

// Build template command with streaming output
console.log(`Building template ${templateName}...`);
const buildStatus = await streamCommandOutput('npx', [
    '@e2b/cli',
    'template',
    'build',
    '--name',
    templateName,
    '-c',
    '/root/.jupyter/start-up.sh'
], {
    ...process.env,
    E2B_API_KEY: process.env.E2B_API_KEY,
    E2B_ACCESS_TOKEN: process.env.E2B_ACCESS_TOKEN
});

if (buildStatus.status.code !== 0) {
    throw new Error(`Build failed with code ${buildStatus.status.code}`);
}

console.log('Template built successfully');

// Read template id from e2b.toml
const e2bToml = await fs.readFile('e2b.toml', 'utf-8');
const templateID = e2bToml.match(/template_id = "(.*)"/)?.[1];

if (!templateID) {
    throw new Error('Template ID not found in e2b.toml');
}

// Remove the file to make script idempotent in local testing
await fs.unlink('e2b.toml');

if (!templateID) {
    throw new Error('Template not found');
}

const sandbox = await Sandbox.create({ id: templateID });

// Execute JavaScript cells
await sandbox.runCode('x = 1');
const execution = await sandbox.runCode('x+=1; x');

console.log('Execution result:', execution);
// kill sandbox
await sandbox.kill();