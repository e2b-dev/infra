import { Sandbox } from "npm:@e2b/code-interpreter";

console.log('creating sandbox')

let sandbox: Sandbox | null = null

try {
    // Create a E2B Code Interpreter with JavaScript kernel
    sandbox = await Sandbox.create();
} catch (error) {
    throw new Error('error creating sandbox', error)
}


console.log('sandbox created')

console.log('running code')

try {
    // Execute JavaScript cells
    await sandbox.runCode('x = 1');
    const execution = await sandbox.runCode('x+=1; x');
    console.log('code executed')
    // Output result
    console.log(execution.text);
} catch (error) {
    throw new Error('error running code', error)
} finally {
    console.log('killing sandbox')
    await sandbox?.kill()
    console.log('sandbox killed')
}

console.log('done') 
