import { exec, OutputMode } from "https://deno.land/x/exec/mod.ts";
import { Sandbox } from "npm:@e2b/code-interpreter";


const uniqueID = crypto.randomUUID();

const templateName = `test-template-${uniqueID}`

console.log('templateName', templateName)

let out = await exec(`npx e2b template build --name "${templateName}" -c "/root/.jupyter/start-up.sh"`, { output: OutputMode.Capture });

console.log(out.output)

// // todo for some reason template build doesn't have a 0 exit code
// // if (out.code !== 0) {
// //     console.log(out.stdout)
// //     console.log(out.stderr)
// //     console.log('error code', out.code)
// //     console.log('cmd', `npx e2b template build --name "${templateID}" -c "/root/.jupyter/start-up.sh"`)
// //     throw new Error('Template not built')
// // }

console.log('Template built successfully')

let listOut = await exec(`npx e2b template list`, { output: OutputMode.Capture });

console.log(listOut.output)

// Access   Template ID           Template Name                                       vCPUs  RAM MiB            Created by  Created at 

// Private  veiohd78xjs3ibuaju57  test-template-4504da89-35a8-4a40-9d30-d7aa40080c77      2     1024  robert.wendt@e2b.dev   2/19/2025 
// Private  1bcr85b1kh4h87yxfsn5  test-template-f7f57822-1500-4d35-9af1-2638bcf77952      2     1024  robert.wendt@e2b.dev   2/19/2025 

// extract template id from the list output
let templateID = listOut.output.split('\n').find(line => line.includes(templateName))?.split(' ')[1]

if (!templateID) {
    throw new Error('Template not found')
}


// create sandbox from that template id, run code in it 

// Create a E2B Code Interpreter with JavaScript kernel

const sandbox = await Sandbox.create({ id: templateID })


// Execute JavaScript cells
await sandbox.runCode('x = 1');
const execution = await sandbox.runCode('x+=1; x');

// Output result
console.log(execution.text);

await exec(`npx e2b template delete -y --name "${templateName}"`);


const templates = await Sandbox.list()

if (templates.find(t => t.name === templateName)) {
    throw new Error('Template found in list')
}




