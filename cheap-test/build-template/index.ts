import { exec } from "https://deno.land/x/exec/mod.ts";
import { Sandbox } from "npm:@e2b/code-interpreter";


const uniqueID = crypto.randomUUID();

const templateID = `test-template-${uniqueID}`

let out = await exec(`npx e2b template build --name "${templateID}" -c "/root/.jupyter/start-up.sh"`);

if (out.code !== 0) {
    console.log(out.stdout)
    console.log(out.stderr)
    console.log('error code', out.code)
    console.log('cmd', `npx e2b template build --name "${templateID}" -c "/root/.jupyter/start-up.sh"`)
    throw new Error('Template not built')
}

console.log('Template built successfully')

let listOut = await exec(`npx e2b template list | grep "${templateID}" > /dev/null`);

if (listOut.code !== 0) {
    throw new Error('Template not found')
}


console.log('Template listed successfully')

// create sandbox from that template id, run code in it 

// Create a E2B Code Interpreter with JavaScript kernel

const sandbox = await Sandbox.create({ id: templateID})


// Execute JavaScript cells
await sandbox.runCode('x = 1');
const execution = await sandbox.runCode('x+=1; x');

// Output result
console.log(execution.text);

let deleteOut = await exec(`npx e2b template delete -y --name "${templateID}"`);

if (deleteOut.code !== 0) {
    throw new Error('Template not deleted')
}


console.log('Template deleted successfully')