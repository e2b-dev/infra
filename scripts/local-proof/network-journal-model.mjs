// Verify the model and check its executable oracle against committed Go fixtures.
// --write intentionally regenerates fixtures; normal verification never changes them.
import { execFileSync } from 'node:child_process'
import { mkdtempSync, readFileSync, writeFileSync, copyFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const root = fileURLToPath(new URL('../../', import.meta.url))
const temporary = mkdtempSync(path.join(tmpdir(), 'sig-network-model-'))
const fixture = path.join(root, 'packages/orchestrator/pkg/sandbox/networkusage/testdata/model.csv')
const dafny = process.env.DAFNY || 'dafny'
if (process.argv.slice(2).some(arg => arg !== '--write')) throw new Error('usage: network-journal-model.mjs [--write]')
try {
  copyFileSync(path.join(root, 'specs/network-usage-journal.dfy'), path.join(temporary, 'model.dfy'))
  const output = execFileSync(dafny, ['run', 'model.dfy', '--target', 'cs', '--verification-time-limit', '30'], {
    cwd: temporary, encoding: 'utf8', timeout: 120_000,
  })
  // Dafny also prints its verification/build summaries. Accept only exact oracle rows.
  const rows = output.split(/\r?\n/).filter(line => /^\d+,(sample|gap|close),\d+,\d+,(true|false),(true|false),\d+,\d+,\d+,(true|false),(true|false),(true|false)$/.test(line))
  if (rows.length === 0) throw new Error(`Dafny emitted no oracle rows: ${output}`)
  const csv = rows.join('\n') + '\n'
  if (process.argv.includes('--write')) writeFileSync(fixture, csv)
  else if (readFileSync(fixture, 'utf8').replace(/\r\n/g, '\n') !== csv) throw new Error('Dafny replay fixture is stale; inspect the model and run --write')
  console.log(output.split(/\r?\n/).filter(line => line.includes('verifier finished')).join('\n'))
  console.log(`Verified model and ${rows.length} replay transitions; fixture ${process.argv.includes('--write') ? 'written' : 'matched'}.`)
} finally {
  // Validate the absolute deletion target stays directly within the temp root.
  if (path.dirname(path.resolve(temporary)) !== path.resolve(tmpdir()) || !path.basename(temporary).startsWith('sig-network-model-')) {
    throw new Error('unexpected model temporary directory')
  }
  rmSync(temporary, { recursive: true, force: true })
}
