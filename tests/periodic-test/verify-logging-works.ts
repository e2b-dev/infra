
import { Sandbox } from "npm:@e2b/code-interpreter";


// Create a E2B Code Interpreter with JavaScript kernel
const sandbox = await Sandbox.create();

let out = ''
console.log('starting command')
// Start the command in the background
const command = await sandbox.commands.run('echo hello; sleep 10; echo world', {
    background: true,
    onStdout: (data) => {
        out += data
    },
})

console.log('waiting for command to finish')
await new Promise(resolve => setTimeout(resolve, 10000))

console.log('killing command')
// Kill the command
await command.kill()

console.log('checking output')
if (!out.includes('hello')) {
    throw new Error('hello not found')
}

if (!out.includes('world')) {
    throw new Error('world not found')
}

console.log('success')
