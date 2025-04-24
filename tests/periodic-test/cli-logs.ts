import { Sandbox } from 'npm:@e2b/code-interpreter'
import { log } from "./utils.ts";

log('Starting sandbox logs test')

let sandbox: Sandbox | null = null

function getLogs(sandboxId: string): Record<string, any>[] {
  const command = new Deno.Command('npx', {
    args: ['@e2b/cli', 'sandbox', 'logs', '--format', 'json', sandboxId],
    stdout: 'piped',
    stderr: 'piped',
  })
  const { stdout } = command.outputSync()
  if (stdout.length === 0) {
    return []
  }

  // Parse JSON lines
  const decoder = new TextDecoder()
  const lines = decoder.decode(stdout).trim().split('\n')
  return lines.map((line) => JSON.parse(line))
}

try {
  // Create sandbox
  log('creating sandbox')
  sandbox = await Sandbox.create()
  log('ℹ️ sandbox created', sandbox.sandboxId)

  // Run a command in the sandbox, so we can test both
  await sandbox.commands.run('echo "Hello, World!"')

  // Kill the sandbox
  log('Killing sandbox')
  await sandbox.kill()

  // It takes some time for logs to be propagated to our log store
  const timeout = 10_000
  const start = Date.now()
  let logsFromAPI = false
  let logsFromSandbox = false
  let logs
  let i = 1

  while (Date.now() - start < timeout && (!logsFromAPI || !logsFromSandbox)) {
    log(`[${i}] Checking logs`)
    logs = getLogs(sandbox.sandboxId)
    for (const log of logs) {
      if (log.logger === 'orchestration-api') {
        logsFromAPI = true
      } else if (log.logger === 'process') {
        logsFromSandbox = true
      }
    }

    i++
    await new Promise((resolve) => setTimeout(resolve, 1000))
  }

  if (!logsFromAPI || !logsFromSandbox) {
    log('Logs:', logs)
    log(
      'Logs from API:',
      logsFromAPI,
      'Logs from sandbox:',
      logsFromSandbox
    )
    throw new Error('Logs not collected')
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
      log('Error closing sandbox:', error)
      throw error
    }
  }
}
