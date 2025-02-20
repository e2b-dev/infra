// exec b/c for some reason npx @e2b/cli template list doesn't work in the workflow with the env vars

const process = Deno.run({
    cmd: ['sh', '-c', 'npx @e2b/cli template list'],
    env: Deno.env.toObject(),
    stdout: 'piped',
    stderr: 'piped'
});

const output = await process.output();
const error = await process.stderrOutput();

console.log('stdout:', new TextDecoder().decode(output));
console.log('stderr:', new TextDecoder().decode(error));

process.close();