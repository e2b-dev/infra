import { randomUUID } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { Sandbox } from 'e2b';
import {
  assertBoundedCapacity,
  collectPaginator,
  normalizeApiUrl,
  parseForkResponse,
  PINNED_E2B_SDK_VERSION,
  requiredEnv,
  safeErrorMessage,
} from './lifecycle-core.mjs';
import {
  TEMPLATE_MARKER_PATH,
  TEMPLATE_MARKER_VALUE,
} from './template-definition.mjs';

const environment = process.env;
const apiKey = requiredEnv(environment, 'E2B_API_KEY');
const apiUrl = normalizeApiUrl(requiredEnv(environment, 'E2B_API_URL'));
const domain = requiredEnv(environment, 'E2B_DOMAIN');
const template = requiredEnv(environment, 'E2B_TEMPLATE');
const packageMetadata = JSON.parse(
  await readFile(new URL('./node_modules/e2b/package.json', import.meta.url), 'utf8'),
);

if (packageMetadata.version !== PINNED_E2B_SDK_VERSION) {
  throw new Error(
    `e2b SDK ${packageMetadata.version} is installed; expected ${PINNED_E2B_SDK_VERSION}`,
  );
}

const runId = `${new Date().toISOString().replaceAll(/[-:.]/g, '').toLowerCase()}-${randomUUID().slice(0, 8)}`;
const snapshotName = `monad-canary-${runId}`;
const markerPath = '/home/user/monad-canary-state';
const requestTimeoutMs = 10 * 60 * 1000;
const sandboxTimeoutMs = 15 * 60 * 1000;
const connection = {
  apiKey,
  apiUrl,
  domain,
  requestTimeoutMs,
};
const createOptions = {
  ...connection,
  timeoutMs: sandboxTimeoutMs,
  allowInternetAccess: false,
  network: {
    denyOut: ['0.0.0.0/0'],
    allowPublicTraffic: false,
  },
  lifecycle: {
    onTimeout: 'pause',
    autoResume: false,
  },
  metadata: {
    'monad.operator.canary': runId,
    'monad.operator.synthetic': 'true',
  },
};

const trackedSandboxIds = new Set();
const trackedSnapshotIds = new Set();
const evidence = {
  run_id: runId,
  sdk_version: packageMetadata.version,
  template,
  api_origin: new URL(apiUrl).origin,
  stages: [],
};

function record(stage, details = {}) {
  evidence.stages.push({ stage, at: new Date().toISOString(), ...details });
}

async function listSandboxes() {
  return collectPaginator(
    Sandbox.list({
      ...connection,
      limit: 100,
      query: { state: ['running', 'paused'] },
    }),
    connection,
  );
}

async function listSnapshots(sandboxId) {
  return collectPaginator(
    Sandbox.listSnapshots({
      ...connection,
      limit: 100,
      ...(sandboxId ? { sandboxId } : {}),
    }),
    connection,
  );
}

async function assertCapacity(expectedMaximum = 2) {
  const sandboxes = await listSandboxes();
  assertBoundedCapacity(sandboxes, expectedMaximum);
  return sandboxes;
}

async function waitForAbsence(sandboxId) {
  for (let attempt = 0; attempt < 30; attempt += 1) {
    const sandboxes = await listSandboxes();
    if (!sandboxes.some((sandbox) => sandbox.sandboxId === sandboxId)) {
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, 2_000));
  }
  throw new Error(`sandbox ${sandboxId} remained after deletion`);
}

async function killAndConfirm(sandboxId) {
  await Sandbox.kill(sandboxId, connection);
  await waitForAbsence(sandboxId);
  trackedSandboxIds.delete(sandboxId);
}

async function runChecked(sandbox, command, expectedStdout) {
  const result = await sandbox.commands.run(command, {
    timeoutMs: 30_000,
    requestTimeoutMs,
  });
  if (
    result.exitCode !== 0 ||
    result.stdout !== expectedStdout ||
    result.stderr !== ''
  ) {
    throw new Error('sandbox command returned unexpected output');
  }
}

async function forkSource(sourceId) {
  const response = await fetch(
    `${apiUrl}/sandboxes/${encodeURIComponent(sourceId)}/fork`,
    {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-API-Key': apiKey,
      },
      body: JSON.stringify({ count: 1, timeout: 900 }),
      signal: AbortSignal.timeout(requestTimeoutMs),
    },
  );

  let payload;
  try {
    payload = await response.json();
  } catch {
    throw new Error(`fork request returned non-JSON HTTP ${response.status}`);
  }

  if (Array.isArray(payload)) {
    for (const entry of payload) {
      const candidate = entry?.sandbox?.sandboxID;
      if (typeof candidate === 'string' && candidate !== '') {
        trackedSandboxIds.add(candidate);
      }
    }
  }
  return parseForkResponse(response.status, payload);
}

async function cleanup() {
  const failures = [];

  try {
    const current = await listSandboxes();
    for (const sandbox of current) {
      trackedSandboxIds.add(sandbox.sandboxId);
    }
  } catch (error) {
    failures.push(`inventory: ${safeErrorMessage(error, [apiKey])}`);
  }

  for (const sandboxId of [...trackedSandboxIds]) {
    try {
      await killAndConfirm(sandboxId);
    } catch (error) {
      failures.push(
        `sandbox ${sandboxId}: ${safeErrorMessage(error, [apiKey])}`,
      );
    }
  }

  try {
    const snapshots = await listSnapshots();
    for (const snapshot of snapshots) {
      if (
        trackedSnapshotIds.has(snapshot.snapshotId) ||
        snapshot.names?.some((name) => name.includes(snapshotName))
      ) {
        trackedSnapshotIds.add(snapshot.snapshotId);
      }
    }
  } catch (error) {
    failures.push(`snapshot inventory: ${safeErrorMessage(error, [apiKey])}`);
  }

  for (const snapshotId of [...trackedSnapshotIds]) {
    try {
      await Sandbox.deleteSnapshot(snapshotId, connection);
    } catch (error) {
      failures.push(
        `snapshot ${snapshotId}: ${safeErrorMessage(error, [apiKey])}`,
      );
    }
  }

  try {
    const remainingSnapshots = await listSnapshots();
    const ownedSnapshots = remainingSnapshots.filter(
      (snapshot) =>
        trackedSnapshotIds.has(snapshot.snapshotId) ||
        snapshot.names?.some((name) => name.includes(snapshotName)),
    );
    if (ownedSnapshots.length > 0) {
      failures.push(`${ownedSnapshots.length} explicit snapshot(s) remain`);
    } else {
      trackedSnapshotIds.clear();
    }
  } catch (error) {
    failures.push(
      `final snapshot inventory: ${safeErrorMessage(error, [apiKey])}`,
    );
  }

  try {
    const remaining = await listSandboxes();
    if (remaining.length !== 0) {
      failures.push(`${remaining.length} sandbox(es) remain`);
    }
  } catch (error) {
    failures.push(`final inventory: ${safeErrorMessage(error, [apiKey])}`);
  }

  if (failures.length > 0) {
    throw new Error(`cleanup incomplete: ${failures.join('; ')}`);
  }
}

let lifecycleError;
try {
  const initial = await assertCapacity(0);
  if (initial.length !== 0) {
    throw new Error(
      `synthetic canary team is not empty: found ${initial.length} sandbox(es)`,
    );
  }
  record('preflight', { active_sandboxes: 0 });

  let source = await Sandbox.create(template, createOptions);
  trackedSandboxIds.add(source.sandboxId);
  await assertCapacity();
  record('source-created', { sandbox_id: source.sandboxId });

  await runChecked(
    source,
    `test "$(cat ${TEMPLATE_MARKER_PATH})" = ${TEMPLATE_MARKER_VALUE} && printf '%s' source > ${markerPath} && printf '%s' source`,
    'source',
  );
  record('source-executed');

  await source.pause(connection);
  source = await Sandbox.connect(source.sandboxId, {
    ...connection,
    timeoutMs: sandboxTimeoutMs,
  });
  await runChecked(source, `cat ${markerPath}`, 'source');
  record('source-paused-and-connected');

  const snapshot = await source.createSnapshot({
    ...connection,
    name: snapshotName,
  });
  trackedSnapshotIds.add(snapshot.snapshotId);
  record('snapshot-created', { snapshot_id: snapshot.snapshotId });

  const restored = await Sandbox.create(snapshot.snapshotId, createOptions);
  trackedSandboxIds.add(restored.sandboxId);
  await assertCapacity();
  await runChecked(restored, `cat ${markerPath}`, 'source');
  await runChecked(
    restored,
    `printf '%s' restored > ${markerPath} && printf '%s' restored`,
    'restored',
  );
  await killAndConfirm(restored.sandboxId);
  record('snapshot-restored-and-destroyed', {
    sandbox_id: restored.sandboxId,
  });

  source = await Sandbox.connect(source.sandboxId, {
    ...connection,
    timeoutMs: sandboxTimeoutMs,
  });
  const forkId = await forkSource(source.sandboxId);
  trackedSandboxIds.add(forkId);
  await assertCapacity();
  const fork = await Sandbox.connect(forkId, {
    ...connection,
    timeoutMs: sandboxTimeoutMs,
  });
  await runChecked(fork, `cat ${markerPath}`, 'source');
  await runChecked(
    fork,
    `printf '%s' fork > ${markerPath} && printf '%s' fork`,
    'fork',
  );
  await runChecked(source, `cat ${markerPath}`, 'source');
  await killAndConfirm(forkId);
  record('fork-diverged-and-destroyed', { sandbox_id: forkId });

  await killAndConfirm(source.sandboxId);
  record('source-destroyed', { sandbox_id: source.sandboxId });

  const snapshotDeleted = await Sandbox.deleteSnapshot(
    snapshot.snapshotId,
    connection,
  );
  if (!snapshotDeleted) {
    throw new Error('explicit snapshot was not found during deletion');
  }
  trackedSnapshotIds.delete(snapshot.snapshotId);
  record('snapshot-destroyed', { snapshot_id: snapshot.snapshotId });
} catch (error) {
  lifecycleError = error;
} finally {
  try {
    await cleanup();
    record('cleanup-confirmed', { active_sandboxes: 0 });
  } catch (cleanupError) {
    if (lifecycleError) {
      lifecycleError = new AggregateError(
        [lifecycleError, cleanupError],
        'lifecycle and cleanup both failed',
      );
    } else {
      lifecycleError = cleanupError;
    }
  }
}

if (lifecycleError) {
  process.stderr.write(
    `operator canary failed: ${safeErrorMessage(lifecycleError, [apiKey])}\n`,
  );
  process.exitCode = 1;
} else {
  process.stdout.write(`${JSON.stringify(evidence, null, 2)}\n`);
}
