import assert from 'node:assert/strict';
import {
  chmod,
  lstat,
  mkdtemp,
  readFile,
  rm,
  symlink,
  writeFile,
} from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';

const pinnedLauncher = '#!/usr/bin/with-contenv bash\n' +
  'setup\n' +
  'exec s6-setuidgid abc \\\n' +
  '  selkies \\\n' +
  '    --addr 0.0.0.0 \\\n' +
  '    --port 3000\n';

test('rewrites the exact pinned multiline Selkies token and preserves metadata', async () => {
  const { rewritePinnedSelkiesLauncher } = await import(
    './selkies-launcher-rewrite.mjs'
  );
  const directory = await mkdtemp(join(tmpdir(), 'monad-selkies-helper-'));
  const launcher = join(directory, 'monad-webtop-svc-selkies');
  try {
    await writeFile(launcher, pinnedLauncher, { mode: 0o555 });
    await chmod(launcher, 0o555);
    const before = await lstat(launcher);

    const result = rewritePinnedSelkiesLauncher(launcher, {
      expectedUid: before.uid,
      expectedGid: before.gid,
    });

    assert.deepEqual(result, {
      before_count: 1,
      after_count: 1,
      metadata: `${before.uid}:${before.gid}:555`,
    });
    assert.equal(
      await readFile(launcher, 'utf8'),
      pinnedLauncher.replace(
        '  selkies \\\n',
        '  /lsiopy/bin/selkies \\\n',
      ),
    );
    const after = await lstat(launcher);
    assert.equal(after.isFile(), true);
    assert.equal(after.isSymbolicLink(), false);
    assert.equal(after.uid, before.uid);
    assert.equal(after.gid, before.gid);
    assert.equal(after.mode & 0o7777, 0o555);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test('fails closed for duplicate, drifted, symlinked, or non-0555 launchers', async () => {
  const { rewritePinnedSelkiesLauncher } = await import(
    './selkies-launcher-rewrite.mjs'
  );
  const directory = await mkdtemp(join(tmpdir(), 'monad-selkies-helper-'));
  const launcher = join(directory, 'monad-webtop-svc-selkies');
  const target = join(directory, 'target');
  const expected = {
    expectedUid: process.getuid(),
    expectedGid: process.getgid(),
  };
  try {
    for (const invalid of [
      pinnedLauncher.replace(
        '  selkies \\\n',
        '  selkies \\\n  selkies \\\n',
      ),
      pinnedLauncher.replace('  selkies \\\n', '  /unexpected/selkies \\\n'),
      pinnedLauncher.replace(
        '  selkies \\\n',
        '  /lsiopy/bin/selkies \\\n',
      ),
    ]) {
      await writeFile(launcher, invalid, { mode: 0o555 });
      await chmod(launcher, 0o555);
      assert.throws(
        () => rewritePinnedSelkiesLauncher(launcher, expected),
        /pinned Selkies launcher contract is invalid/,
      );
      assert.equal(await readFile(launcher, 'utf8'), invalid);
      await chmod(launcher, 0o644);
    }

    await writeFile(launcher, pinnedLauncher, { mode: 0o644 });
    assert.throws(
      () => rewritePinnedSelkiesLauncher(launcher, expected),
      /pinned Selkies launcher metadata is invalid/,
    );

    await writeFile(target, pinnedLauncher, { mode: 0o555 });
    await chmod(target, 0o555);
    await rm(launcher, { force: true });
    await symlink(target, launcher);
    assert.throws(
      () => rewritePinnedSelkiesLauncher(launcher, expected),
      /pinned Selkies launcher metadata is invalid/,
    );
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
