import { Sandbox } from "npm:@e2b/code-interpreter";


// Create a E2B Code Interpreter with JavaScript kernel
const sandbox = await Sandbox.create();

// Execute JavaScript cells
await sandbox.runCode('x = 1');
const execution = await sandbox.runCode('x+=1; x');

// Output result
console.log(execution.text);
