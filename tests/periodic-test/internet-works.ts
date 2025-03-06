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


  const stdout = out.stdout
  //   ➜  orchestrator git:(main) ✗ ping -c 3 8.8.8.8                                         ~/infra/packages/orchestrator
  // PING 8.8.8.8 (8.8.8.8): 56 data bytes
  // 64 bytes from 8.8.8.8: icmp_seq=0 ttl=116 time=8.866 ms
  // 64 bytes from 8.8.8.8: icmp_seq=1 ttl=116 time=7.994 ms
  // 64 bytes from 8.8.8.8: icmp_seq=2 ttl=116 time=8.532 ms

  // --- 8.8.8.8 ping statistics ---
  // 3 packets transmitted, 3 packets received, 0.0% packet loss
  // round-trip min/avg/max/stddev = 7.994/8.464/8.866/0.359 ms
  const internetWorking = stdout.includes('0.0% packet loss')
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
