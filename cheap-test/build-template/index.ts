import { exec } from "https://deno.land/x/exec/mod.ts";

const uniqueID = crypto.randomUUID();

let out = await exec(`npx e2b template build --name "test-template-${uniqueID}" -c "/root/.jupyter/start-up.sh"`);

if (out.code !== 0) {
    console.log(out.stdout)
    console.log(out.stderr)
    console.log('error code', out.code)
    console.log('cmd', `npx e2b template build --name "test-template-${uniqueID}" -c "/root/.jupyter/start-up.sh"`)
    throw new Error('Template not built')
}




console.log('Template built successfully')

let listOut = await exec(`npx e2b template list | grep "test-template-${uniqueID}" > /dev/null`);

if (listOut.code !== 0) {
    throw new Error('Template not found')
}


console.log('Template listed successfully')

let deleteOut = await exec(`npx e2b template delete -y --name "test-template-${uniqueID}"`);

if (deleteOut.code !== 0) {
    throw new Error('Template not deleted')
}

console.log('Template deleted successfully')