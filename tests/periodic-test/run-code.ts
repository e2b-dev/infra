import { Sandbox } from "npm:@e2b/code-interpreter";
import { log } from "./utils.ts";


let sandbox: Sandbox | null = null

try {
    // Create a E2B Code Interpreter with JavaScript kernel
    log('creating sandbox')
    sandbox = await Sandbox.create();
    log('ℹ️ sandbox created', sandbox.sandboxId)
} catch (error) {
    log('Test failed:', error)
    throw new Error('error creating sandbox', error)
}



try {
    // Execute JavaScript cells
    log('running code')
    await sandbox.runCode('x = 1');
    const execution = await sandbox.runCode('x+=1; x');
    log('code executed')
    // Output result
    log(execution.text);
} catch (error) {
    log('Test failed:', error)
    throw new Error('error running code', error)
} finally {
    log('killing sandbox')
    await sandbox?.kill()
    log('sandbox killed')
}

log('done') 
