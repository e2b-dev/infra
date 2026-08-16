import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import {
  chmod,
  mkdtemp,
  open,
  rename,
  rm,
  symlink,
  truncate,
  writeFile,
} from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';

import {
  attestPreparedEntrypoint,
  attestPreparedExecutable,
} from './prepare-assets.mjs';

function amd64ElfFixture() {
  const value = Buffer.alloc(64);
  value.set([0x7f, 0x45, 0x4c, 0x46], 0);
  value.set([0x3e, 0x00], 18);
  return value;
}

test('attests and hashes a regular executable through one no-follow descriptor', async () => {
  const fixture = await mkdtemp(join(tmpdir(), 'monad-prepared-executable-'));
  try {
    const executable = join(fixture, 'agent');
    await writeFile(executable, amd64ElfFixture());
    await chmod(executable, 0o755);
    assert.equal(
      await attestPreparedExecutable(executable),
      createHash('sha256').update(amd64ElfFixture()).digest('hex'),
    );
  } finally {
    await rm(fixture, { recursive: true, force: true });
  }
});

test('attests and hashes the exact regular entrypoint through one no-follow descriptor', async () => {
  const fixture = await mkdtemp(join(tmpdir(), 'monad-prepared-entrypoint-'));
  try {
    const entrypoint = join(fixture, 'entrypoint.sh');
    const bytes = Buffer.from('#!/bin/bash\nexec /opt/monad/runtime/bin/monad-agent "$@"\n');
    await writeFile(entrypoint, bytes);
    await chmod(entrypoint, 0o755);
    assert.equal(
      await attestPreparedEntrypoint(entrypoint),
      createHash('sha256').update(bytes).digest('hex'),
    );
  } finally {
    await rm(fixture, { recursive: true, force: true });
  }
});

test('rejects a symlink instead of following it during executable attestation', async () => {
  const fixture = await mkdtemp(join(tmpdir(), 'monad-prepared-executable-'));
  try {
    const executable = join(fixture, 'agent');
    const alias = join(fixture, 'agent-link');
    await writeFile(executable, amd64ElfFixture());
    await chmod(executable, 0o755);
    await symlink(executable, alias);
    await assert.rejects(
      attestPreparedExecutable(alias),
      /regular no-follow file/,
    );
  } finally {
    await rm(fixture, { recursive: true, force: true });
  }
});

async function largeExecutable(path, bytes = 128 * 1024 * 1024) {
  await writeFile(path, amd64ElfFixture());
  await truncate(path, bytes);
  await chmod(path, 0o755);
}

test('rejects an executable mutated while its descriptor is being hashed', async () => {
  const fixture = await mkdtemp(join(tmpdir(), 'monad-prepared-executable-'));
  let stop = false;
  let mutate;
  try {
    const executable = join(fixture, 'agent');
    await largeExecutable(executable);
    const mutator = await open(executable, 'r+');
    mutate = (async () => {
      let value = 0;
      while (!stop) {
        await mutator.write(Buffer.from([value++ & 0xff]), 0, 1, 1024 * 1024);
        await new Promise((resolve) => setImmediate(resolve));
      }
      await mutator.close();
    })();
    await assert.rejects(
      attestPreparedExecutable(executable),
      /changed during attestation/,
    );
  } finally {
    stop = true;
    await mutate;
    await rm(fixture, { recursive: true, force: true });
  }
});

test('rejects a pathname replaced after the executable descriptor is opened', async () => {
  const fixture = await mkdtemp(join(tmpdir(), 'monad-prepared-executable-'));
  try {
    const executable = join(fixture, 'agent');
    const replacement = join(fixture, 'replacement');
    const displaced = join(fixture, 'displaced');
    await Promise.all([
      largeExecutable(executable, 256 * 1024 * 1024),
      largeExecutable(replacement, 256 * 1024 * 1024),
    ]);
    const attestation = attestPreparedExecutable(executable);
    await new Promise((resolve) => setTimeout(resolve, 5));
    await rename(executable, displaced);
    await rename(replacement, executable);
    await assert.rejects(attestation, /changed during attestation/);
  } finally {
    await rm(fixture, { recursive: true, force: true });
  }
});
