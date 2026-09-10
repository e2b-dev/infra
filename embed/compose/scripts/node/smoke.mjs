import { Sandbox } from 'e2b'

for (const name of ['E2B_API_URL', 'E2B_API_KEY', 'E2B_SANDBOX_URL']) {
  if (!process.env[name]) {
    console.error(`smoke: ${name} is required`)
    console.error('FIX: export the E2B_API_URL, E2B_SANDBOX_URL and E2B_API_KEY values the ready container prints')
    process.exit(2)
  }
}

let sbx
let failed = false
try {
  sbx = await Sandbox.create('base', { timeoutMs: 120_000 })
  const result = await sbx.commands.run('echo hello')
  if (result.exitCode !== 0 || result.stdout.trim() !== 'hello') {
    throw new Error(`unexpected command result: ${JSON.stringify(result)}`)
  }
  console.log(`smoke: ok sandbox=${sbx.sandboxId} host=${sbx.getHost(8080)}`)
} catch (err) {
  console.error('smoke: failed:', err)
  failed = true
  process.exitCode = 1
} finally {
  // Ending the sandbox is part of what this test checks, so a kill failure is
  // still a failure: the exit code stays non-zero. It must read as a FIX line
  // though, never as an unhandled rejection out of the finally block. The FIX
  // lines are printed here, after the kill, so that when the test itself
  // failed its own FIX line is the last thing on stderr.
  let killFailed = false
  if (sbx) {
    try {
      await sbx.kill()
    } catch (err) {
      console.error('smoke: the sandbox could not be killed:', err)
      killFailed = true
      process.exitCode = 1
    }
  }
  if (failed) {
    console.error('FIX: read the api, orchestrator and client-proxy logs')
  } else if (killFailed) {
    console.error('FIX: the sandbox could not be killed; read the api and orchestrator logs')
  }
}
