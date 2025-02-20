import { Sandbox } from "npm:@e2b/code-interpreter";
import { extractTemplateID } from "./extract.ts";

// Helper function to stream command output
async function streamCommandOutput(command: string, args: string[]) {
    const cmd = new Deno.Command(command, {
        args: args,
        stdout: "piped",
        stderr: "piped",
    });

    const process = cmd.spawn();
    const decoder = new TextDecoder();

    let output = ''

    // Stream stdout
    for await (const chunk of process.stdout) {
        console.log(decoder.decode(chunk));
        output += decoder.decode(chunk)
    }

    // Stream stderr
    for await (const chunk of process.stderr) {
        console.error(decoder.decode(chunk));
        output += decoder.decode(chunk)
    }

    // Wait for the process to complete and get the status
    const status = await process.status;
    return { status, output }
}

const uniqueID = crypto.randomUUID();
const templateName = `test-template-${uniqueID}`
console.log('templateName:', templateName)

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
]);

if (buildStatus.status.code !== 0) {
    throw new Error(`Build failed with code ${buildStatus.status.code}`);
}

console.log('Template built successfully')


const templates = await streamCommandOutput('npx', [
    '@e2b/cli',
    'template',
    'list'
])
const templateID = extractTemplateID(templates.output, templateName)

if (!templateID) {
    throw new Error('Template not found')
}

const sandbox = await Sandbox.create({ id: templateID })

// Execute JavaScript cells
await sandbox.runCode('x = 1');
const execution = await sandbox.runCode('x+=1; x');

if (execution !== '2') {
    throw new Error('Execution failed')
}

// Output result
console.log('Execution result:', execution.text);

// kill sandbox
await sandbox.kill()


// cleanup by removing e2b.toml
await Deno.remove('e2b.toml')