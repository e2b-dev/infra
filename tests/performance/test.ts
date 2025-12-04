import { Sandbox } from 'e2b'

interface MeasureSandboxResult {
  startTimeMs: number
  speedBytesPerSec?: number
}

function parseDdSpeed(output: string): number {
  // Match number with optional decimal, followed by unit (KB/s, MB/s, or GB/s)
  const match = output.match(/(\d+(?:\.\d+)?)\s*(KB|MB|GB)\/s/)
  if (!match) {
    throw new Error('Could not parse speed from output')
  }

  const speed = parseFloat(match[1])
  const unit = match[2]

  switch (unit) {
    case 'KB':
      return speed * 1024
    case 'MB':
      return speed * 1024 * 1024
    case 'GB':
      return speed * 1024 * 1024 * 1024
    default:
      throw new Error(`Unknown unit: ${unit}`)
  }
}

interface Workload {
  (sbx: Sandbox): Promise<number>
}

async function measureSandbox(workload?: Workload): Promise<MeasureSandboxResult> {
  const sbxStartTime = Date.now()
  const sbx = await Sandbox.create('test-pci', { timeoutMs: 1 * 60 * 1000 })
  const sbxStartEndTime = Date.now()

  try {
    if (workload) {
      return {
        startTimeMs: sbxStartEndTime - sbxStartTime,
        speedBytesPerSec: await workload(sbx),
      }
    }
  } finally {
    await sbx.kill()
  }

  return {
    startTimeMs: sbxStartEndTime - sbxStartTime,
  }
}


async function averageMeasurement(iterations: number, workload?: Workload): Promise<MeasureSandboxResult> {
  const results: MeasureSandboxResult[] = []

  for (let i = 0; i < iterations; i++) {
    results.push(await measureSandbox(workload))
  }

  return {
    startTimeMs: Math.round(results.reduce((acc, curr) => acc + curr.startTimeMs, 0) / iterations),
    speedBytesPerSec: Math.round(results.map(r => r.speedBytesPerSec || 0).reduce((acc, curr) => acc + curr, 0) / iterations),
  }
}

const iterations = 10

const averageStart = await averageMeasurement(iterations)
console.log(`Average start time: ${averageStart.startTimeMs}ms`)

const device = '/dev/vda'
const size = 1024 * 1024 * 1024
const chunk = 512 * 1024 * 1024
const sizeInChunks = size / chunk
const file = 'test-file.txt'


async function averageDdMeasurement(ddCommand: string): Promise<MeasureSandboxResult> {
  return await averageMeasurement(iterations, async (sbx) =>
    await sbx.commands.run(ddCommand, { user: 'root' }).then(result => parseDdSpeed(result.stderr))
  )
}

const ddCommands: Record<string, string> = {
  '4KBRead': `dd if=${device} of=/dev/null bs=4k count=${sizeInChunks}`,
  '1MBRead': `dd if=${device} of=/dev/null bs=1M count=${sizeInChunks}`,
  '100MBRead': `dd if=${device} of=/dev/null bs=100M count=${sizeInChunks}`,
  '4KBWrite': `dd if=/dev/random of=${file} bs=4k count=${sizeInChunks}`,
  '1MBWrite': `dd if=/dev/random of=${file} bs=1M count=${sizeInChunks}`,
  '100MBWrite': `dd if=/dev/random of=${file} bs=100M count=${sizeInChunks}`,
}

const ddResults: Record<string, MeasureSandboxResult> = {}

for (const [name, command] of Object.entries(ddCommands)) {
  const result = await averageDdMeasurement(command)
  ddResults['average' + name.charAt(0).toUpperCase() + name.slice(1)] = result
  console.log(`Average ${name}: ${Math.round(result.speedBytesPerSec! / 1024 / 1024)} MB/s`)
}

function cloudflareUploadCommand(sizeMb: number): string {
  return `head -c ${sizeMb}M /dev/zero | curl -X POST https://speed.cloudflare.com/__up \
  --data-binary @- \
  -w "%{speed_upload}\\n"`;
}

function cloudflareDownloadCommand(sizeMb: number): string {
  return `curl -w "%{speed_download}\n" \ https://speed.cloudflare.com/__down?bytes=${sizeMb * 1024 * 1024} -o /dev/null`
}

function parseCloudflareSpeed(output: string): number {
  const lines = output.trim().split('\n').filter(line => line.trim().length > 0);
  const lastLine = lines[lines.length - 1];
  const speedBytesPerSec = parseInt(lastLine, 10);
  if (isNaN(speedBytesPerSec)) {
    throw new Error('Could not parse speed from output');
  }

  return speedBytesPerSec;
}

async function averageCloudflareMeasurement(command: string): Promise<MeasureSandboxResult> {
  return await averageMeasurement(iterations, async (sbx) =>
    await sbx.commands.run(command, { user: 'root' }).then(result => parseCloudflareSpeed(result.stdout))
  )
}

// Test with upload > 256 seems to be killed by Cloudflare.
const cloudflareCommands: Record<string, string> = {
  '1MBDownload': cloudflareDownloadCommand(1),
  '128MBDownload': cloudflareDownloadCommand(128),
  '256MBDownload': cloudflareDownloadCommand(256),
  '1MBUpload': cloudflareUploadCommand(1),
  '128MBUpload': cloudflareUploadCommand(128),
  '256MBUpload': cloudflareUploadCommand(256),
}

const cloudflareResults: Record<string, MeasureSandboxResult> = {}

for (const [name, command] of Object.entries(cloudflareCommands)) {
  const result = await averageCloudflareMeasurement(command)
  cloudflareResults['average' + name.charAt(0).toUpperCase() + name.slice(1)] = result
  console.log(`Average ${name}: ${Math.round(result.speedBytesPerSec! / 1024 / 1024)} MB/s`)
}

const resultsJson = {
  averageStartTimeMs: averageStart.startTimeMs,
  ...ddResults,
  ...cloudflareResults,
}

console.log(resultsJson)