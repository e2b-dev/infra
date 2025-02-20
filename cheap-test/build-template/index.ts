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

    // Stream stdout
    for await (const chunk of process.stdout) {
        console.log(decoder.decode(chunk));
    }

    // Stream stderr
    for await (const chunk of process.stderr) {
        console.error(decoder.decode(chunk));
    }

    // Wait for the process to complete and get the status
    const status = await process.status;
    return status;
}

const uniqueID = crypto.randomUUID();
const templateName = `test-template-${uniqueID}`
console.log('templateName:', templateName)

// Echo command with streaming output
console.log('Running echo test...');
const echoStatus = await streamCommandOutput('echo', ['hello world']);
if (echoStatus.code !== 0) {
    throw new Error(`Echo command failed with code ${echoStatus.code}`);
}

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

if (buildStatus.code !== 0) {
    throw new Error(`Build failed with code ${buildStatus.code}`);
}

console.log('Template built successfully')

const templates = await Sandbox.list()
console.log('Available templates:', templates)

// Create a E2B Code Interpreter with JavaScript kernel
const templateID = templates.find(t => t.name === templateName)?.id;

if (!templateID) {
    throw new Error('Template not found')
}

const sandbox = await Sandbox.create({ id: templateID })

// Execute JavaScript cells
await sandbox.runCode('x = 1');
const execution = await sandbox.runCode('x+=1; x');

// Output result
console.log('Execution result:', execution.text);

// Delete template command with streaming output
console.log(`Deleting template ${templateName}...`);
const deleteStatus = await streamCommandOutput('npx', [
    '@e2b/cli',
    'template',
    'delete',
    '-y',
    '--name',
    templateName
]);

if (deleteStatus.code !== 0) {
    throw new Error(`Delete failed with code ${deleteStatus.code}`);
}

const templatesAfterDelete = await Sandbox.list()
if (templatesAfterDelete.find(t => t.name === templateName)) {
    throw new Error('Template still found in list after deletion')
}

// cleanup by removing e2b.toml
await Deno.remove('e2b.toml')