import { Sandbox } from "npm:@e2b/code-interpreter@beta";
const sbx = await Sandbox.create()
console.log(new Date().toISOString(), 'Sandbox created', sbx.sandboxId)
await sbx.runCode('x = 1');
console.log(new Date().toISOString(), 'Sandbox code executed')
const sandboxId = await sbx.pause()
console.log(new Date().toISOString(), 'Sandbox paused', sandboxId)


// Resume the sandbox from the same state
const sameSbx = await Sandbox.resume(sandboxId)
console.log(new Date().toISOString(), 'Sandbox resumed', sameSbx.sandboxId)

const execution = await sameSbx.runCode('x+=1; x');

// Output result
console.log(new Date().toISOString(), execution.text);

if (execution.text !== '2') {
    throw new Error('Failed to resume sandbox')
}

console.log(new Date().toISOString(), 'Sandbox resumed successfully')

await sbx.kill()
console.log(new Date().toISOString(), 'Sandbox deleted')
