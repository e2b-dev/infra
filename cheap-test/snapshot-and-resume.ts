import { Sandbox } from "npm:@e2b/code-interpreter@beta";


const sbx = await Sandbox.create()
console.log('Sandbox created', sbx.sandboxId)


await sbx.runCode('x = 1');
// Pause the sandbox
// You can save the sandbox ID in your database
// to resume the sandbox later
const sandboxId = await sbx.pause()
console.log('Sandbox paused', sandboxId)

// Resume the sandbox from the same state
const sameSbx = await Sandbox.resume(sandboxId)
console.log('Sandbox resumed', sameSbx.sandboxId)

const execution = await sameSbx.runCode('x+=1; x');

// Output result
console.log(execution.text);

if (execution.text !== '2') {
    throw new Error('Failed to resume sandbox')
}

console.log('Sandbox resumed successfully')


await sbx.stop()
console.log('Sandbox stopped')

await sbx.delete()
console.log('Sandbox deleted')


