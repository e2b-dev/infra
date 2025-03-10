import { Sandbox } from 'npm:@e2b/code-interpreter'

console.log('Starting sandbox logs test')

let sandbox: Sandbox | null = null


if (Deno.env.get('E2B_DOMAIN') === 'e2b-juliett.dev') {
  console.log('Skipping test on juliett.dev b/c internet is disabled')
  Deno.exit(0)
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
