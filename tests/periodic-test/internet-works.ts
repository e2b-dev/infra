import { Sandbox } from 'npm:@e2b/code-interpreter'
import { log } from "./utils.ts";

log('Starting sandbox logs test')

let sandbox: Sandbox | null = null


if (Deno.env.get('E2B_DOMAIN') === 'e2b-juliett.dev') {
  log('Skipping test on juliett.dev b/c internet is disabled')
  Deno.exit(0)
}

try {
  // Create sandbox
  log('creating sandbox')
  sandbox = await Sandbox.create()
  log('ℹ️ sandbox created', sandbox.sandboxId)

  const out = await sandbox.commands.run('wget https://google.com', {
    requestTimeoutMs: 10000,
  })
  log('wget output', out.stderr)


  const internetWorking = out.stderr.includes('200 OK')
  // verify internet is working 
  if (!internetWorking) {
    log('Internet is not working')
    throw new Error('Internet is not working')
  }

  log('Test passed successfully')
} catch (error) {
  log('Test failed:', error)
  throw error
} finally {
  if (sandbox) {
    try {
      await sandbox.kill()
    } catch (error) {
      console.error('Error closing sandbox:', error)
    }
  }
}
