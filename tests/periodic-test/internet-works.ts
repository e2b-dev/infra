import { Sandbox } from 'npm:@e2b/code-interpreter'

console.log('Starting sandbox logs test')

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
  console.log('creating sandbox')
  sandbox = await Sandbox.create()
  console.log('Sandbox created with ID:', sandbox.sandboxId)

  // install ping
  await sandbox.commands.run('sudo apt-get install -y iputils-ping')

  const out = await sandbox.commands.run('ping -c 3 8.8.8.8')
  console.log('ping output', out.stdout)

  const stdout = out.stdout

  const internetWorking = stdout.includes('3 packets transmitted, 3 received, 0% packet loss')
  // verify internet is working 
  if (!internetWorking) {
    throw new Error('Internet is not working')
  }

  console.log('Test passed successfully')
} catch (error) {
  console.error('Test failed:', error)
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
