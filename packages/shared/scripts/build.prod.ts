import { Template } from "e2b";
import { template } from "./template.js";

async function main() {
  await Template.build(template, {
    alias: "base",
    memoryMB: 512,
    skipCache: process.env.SKIP_CACHE !== 'false',
    onBuildLogs: (it) => console.log(it.toString()),
  });
}

main().catch((err) => console.error(err));
