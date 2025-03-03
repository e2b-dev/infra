import { Sandbox } from "npm:@e2b/code-interpreter";


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

// read template id from e2b.toml
const e2bToml = await Deno.readTextFile('e2b.toml')
const templateID = e2bToml.match(/template_id = "(.*)"/)?.[1]

if (!templateID) {
    throw new Error('Template ID not found in e2b.toml')
}

// remove the file to make script idempotent in local testing
await Deno.remove('e2b.toml')

if (!templateID) {
    throw new Error('Template not found')
}

const sandbox = await Sandbox.create({ id: templateID })

// Execute JavaScript cells
await sandbox.runCode('x = 1');
const execution = await sandbox.runCode('x+=1; x');

console.log('Execution result:', execution);
// kill sandbox
await sandbox.kill()


// delete template
const output = await streamCommandOutput('npx', [
    '@e2b/cli',
    'template',
    'delete',
    '-y',
    templateID
])


if (output.status.code !== 0) {
    throw new Error(`Delete failed with code ${output.status.code}`);
}

console.log('Template deleted successfully')