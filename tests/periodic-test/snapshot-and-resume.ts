import { Sandbox } from "npm:@e2b/code-interpreter@beta";
import { log } from "./utils.ts";

const sbx = await Sandbox.create()
log('ℹ️ sandbox created', sbx.sandboxId)

await sbx.runCode('x = 1');
log('Sandbox code executed')

const sandboxId = await sbx.pause()
log('Sandbox paused', sandboxId)

// Resume the sandbox from the same state
const sameSbx = await Sandbox.resume(sandboxId)
log('Sandbox resumed', sameSbx.sandboxId)

const execution = await sameSbx.runCode('x+=1; x');
// Output result
log(execution.text);

if (execution.text !== '2') {
    log('Test failed:', 'Failed to resume sandbox')
    throw new Error('Failed to resume sandbox')
}
log('Sandbox resumed successfully')

await sbx.kill()
log('Sandbox deleted')
