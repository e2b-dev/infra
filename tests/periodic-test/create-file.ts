import { Sandbox } from "npm:@e2b/code-interpreter";

const sandbox = await Sandbox.create()
await sandbox.filesystem.write('/hello.txt', 'Hello World')
const result = await sandbox.filesystem.read('/hello.txt')

if (result !== 'Hello World') {
    throw new Error('Failed to create file')
}

console.log('File created successfully')
