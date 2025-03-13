import { Sandbox } from "npm:@e2b/code-interpreter";

console.log(new Date().toISOString(), 'creating sandbox')

let sandbox: Sandbox | null = null

try {
    // Create a E2B Code Interpreter with JavaScript kernel
    sandbox = await Sandbox.create();
} catch (error) {
    throw new Error('error creating sandbox', error)
}


console.log(new Date().toISOString(), 'sandbox created')

console.log(new Date().toISOString(), 'running code')

try {
    // Execute JavaScript cells
    await sandbox.runCode('x = 1');
    const execution = await sandbox.runCode('x+=1; x');
    console.log(new Date().toISOString(), 'code executed')
    // Output result
    console.log(new Date().toISOString(), execution.text);
} catch (error) {
    throw new Error('error running code', error)
} finally {
    console.log(new Date().toISOString(), 'killing sandbox')
    await sandbox?.kill()
    console.log(new Date().toISOString(), 'sandbox killed')
}

console.log(new Date().toISOString(), 'done') 
