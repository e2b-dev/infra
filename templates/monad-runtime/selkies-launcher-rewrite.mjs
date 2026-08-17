import {
  chmodSync,
  lstatSync,
  readFileSync,
  renameSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { pathToFileURL } from 'node:url';

const LINE_FEED = String.fromCharCode(10);
const CONTINUATION = String.fromCharCode(92);
const BEFORE = `  selkies ${CONTINUATION}`;
const AFTER = `  /lsiopy/bin/selkies ${CONTINUATION}`;

function metadata(path, expectedUid, expectedGid) {
  let value;
  try {
    value = lstatSync(path);
  } catch {
    throw new Error('pinned Selkies launcher metadata is invalid');
  }
  if (
    !value.isFile() ||
    value.isSymbolicLink() ||
    value.uid !== expectedUid ||
    value.gid !== expectedGid ||
    (value.mode & 0o7777) !== 0o555
  ) {
    throw new Error('pinned Selkies launcher metadata is invalid');
  }
  return `${value.uid}:${value.gid}:${(value.mode & 0o7777).toString(8)}`;
}

function exactCount(lines, expected) {
  return lines.reduce(
    (count, line) => count + Number(line === expected),
    0,
  );
}

export function rewritePinnedSelkiesLauncher(
  launcherPath,
  { expectedUid = 0, expectedGid = 0 } = {},
) {
  if (
    typeof launcherPath !== 'string' ||
    launcherPath.length === 0 ||
    !Number.isSafeInteger(expectedUid) ||
    expectedUid < 0 ||
    !Number.isSafeInteger(expectedGid) ||
    expectedGid < 0
  ) {
    throw new Error('pinned Selkies launcher options are invalid');
  }
  metadata(launcherPath, expectedUid, expectedGid);

  let source;
  try {
    source = readFileSync(launcherPath, 'utf8');
  } catch {
    throw new Error('pinned Selkies launcher contract is invalid');
  }
  const lines = source.split(LINE_FEED);
  const beforeCount = exactCount(lines, BEFORE);
  const existingAfterCount = exactCount(lines, AFTER);
  if (beforeCount !== 1 || existingAfterCount !== 0) {
    throw new Error('pinned Selkies launcher contract is invalid');
  }
  lines[lines.indexOf(BEFORE)] = AFTER;

  const replacement = `${launcherPath}.rewrite`;
  try {
    writeFileSync(replacement, lines.join(LINE_FEED), {
      flag: 'wx',
      mode: 0o555,
    });
    chmodSync(replacement, 0o555);
    renameSync(replacement, launcherPath);
  } catch {
    rmSync(replacement, { force: true });
    throw new Error('pinned Selkies launcher rewrite failed');
  }

  const finalMetadata = metadata(launcherPath, expectedUid, expectedGid);
  const finalLines = readFileSync(launcherPath, 'utf8').split(LINE_FEED);
  const finalBeforeCount = exactCount(finalLines, BEFORE);
  const finalAfterCount = exactCount(finalLines, AFTER);
  if (finalBeforeCount !== 0 || finalAfterCount !== 1) {
    throw new Error('pinned Selkies launcher contract is invalid');
  }
  return {
    before_count: beforeCount,
    after_count: finalAfterCount,
    metadata: finalMetadata,
  };
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  try {
    if (process.argv.length !== 3) {
      throw new Error('pinned Selkies launcher options are invalid');
    }
    const evidence = rewritePinnedSelkiesLauncher(process.argv[2]);
    process.stdout.write(`${JSON.stringify(evidence)}${LINE_FEED}`);
  } catch (error) {
    process.stderr.write(
      `${error instanceof Error ? error.message : 'pinned Selkies launcher rewrite failed'}${LINE_FEED}`,
    );
    process.exitCode = 1;
  }
}
