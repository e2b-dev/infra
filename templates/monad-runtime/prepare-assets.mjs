import { execFileSync } from 'node:child_process';
import { cp, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { calculateRuntimeVersion, loadRuntimeSource, requiredEnv } from './runtime-core.mjs';

const templateDir = fileURLToPath(new URL('.', import.meta.url));
const assetsDir = fileURLToPath(new URL('./.build-assets/', import.meta.url));

function run(command, args, options = {}) {
  return execFileSync(command, args, {
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'inherit'],
    ...options,
  }).trim();
}

function git(checkout, ...args) {
  return run('git', ['-C', checkout, ...args]);
}

async function copyContainerFile(image, source, destination, containerName) {
  run('docker', [
    'create',
    '--platform',
    'linux/amd64',
    '--name',
    containerName,
    image,
    'true',
  ]);
  try {
    run('docker', ['cp', `${containerName}:${source}`, destination]);
  } finally {
    run('docker', ['rm', '-f', containerName]);
  }
}

export async function prepareRuntimeAssets(environment = process.env) {
  const tamsCheckout = requiredEnv(environment, 'TAMS_CHECKOUT');
  const source = await loadRuntimeSource();
  const revision = git(tamsCheckout, 'rev-parse', 'HEAD');
  if (revision !== source.tams_revision) {
    throw new Error(
      `TAMS_CHECKOUT is ${revision}; expected ${source.tams_revision}`,
    );
  }
  if (git(tamsCheckout, 'status', '--porcelain') !== '') {
    throw new Error('TAMS_CHECKOUT must be clean');
  }

  const appsSandboxTree = git(
    tamsCheckout,
    'rev-parse',
    'HEAD:apps/sandbox',
  );
  if (appsSandboxTree !== source.tams_apps_sandbox_tree_oid) {
    throw new Error(
      `apps/sandbox tree is ${appsSandboxTree}; expected ${source.tams_apps_sandbox_tree_oid}`,
    );
  }

  const runtimeInputTreeOids = Object.fromEntries(
    source.runtime_input_paths.map((path) => [
      path,
      git(tamsCheckout, 'rev-parse', `HEAD:${path}`),
    ]),
  );
  const sourceWithTrees = {
    ...source,
    runtime_input_tree_oids: runtimeInputTreeOids,
  };
  const runtimeVersion = await calculateRuntimeVersion(sourceWithTrees);
  const shortVersion = runtimeVersion.slice(0, 12);
  const agentImage = `monad-runtime-agent-build:${shortVersion}`;
  const cliImage = `monad-runtime-cli-build:${shortVersion}`;
  const agentContainer = `monad-runtime-agent-${shortVersion}`;
  const cliContainer = `monad-runtime-cli-${shortVersion}`;

  await rm(assetsDir, { recursive: true, force: true });
  await mkdir(assetsDir, { recursive: true });

  try {
    run('docker', [
      'build',
      '--platform',
      'linux/amd64',
      '--target',
      'builder',
      '--tag',
      agentImage,
      '--file',
      'apps/sandbox/Dockerfile',
      '.',
    ], { cwd: tamsCheckout });
    run('docker', [
      'build',
      '--platform',
      'linux/amd64',
      '--target',
      'cli-builder',
      '--tag',
      cliImage,
      '--file',
      'apps/sandbox/Dockerfile',
      '.',
    ], { cwd: tamsCheckout });

    await copyContainerFile(
      agentImage,
      '/agent/dist/monad-agent',
      `${assetsDir}/monad-agent`,
      agentContainer,
    );
    await copyContainerFile(
      cliImage,
      '/cli/monad',
      `${assetsDir}/monad`,
      cliContainer,
    );
  } finally {
    for (const image of [agentImage, cliImage]) {
      try {
        run('docker', ['image', 'rm', '-f', image]);
      } catch {
        // A failed build may not have created both temporary images.
      }
    }
  }

  await cp(
    `${tamsCheckout}/apps/sandbox/entrypoint.sh`,
    `${assetsDir}/entrypoint.sh`,
  );
  await cp(
    `${tamsCheckout}/apps/sandbox/agent-cli`,
    `${assetsDir}/agent-cli`,
    { recursive: true },
  );
  await cp(
    `${tamsCheckout}/packages/executor-sdk`,
    `${assetsDir}/executor-sdk`,
    { recursive: true },
  );

  const manifest = {
    prepared_at: new Date().toISOString(),
    tams_revision: revision,
    tams_apps_sandbox_tree_oid: appsSandboxTree,
    runtime_input_tree_oids: runtimeInputTreeOids,
    runtime_version: runtimeVersion,
  };
  await writeFile(
    `${assetsDir}/manifest.json`,
    `${JSON.stringify(manifest, null, 2)}\n`,
  );

  const dockerfile = await readFile(`${templateDir}/e2b.Dockerfile`, 'utf8');
  if (/\b(?:kasm|selkies|vnc)\b/i.test(dockerfile)) {
    throw new Error('PR A Dockerfile must not contain a desktop layer');
  }
  return manifest;
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const manifest = await prepareRuntimeAssets();
  process.stdout.write(`${JSON.stringify(manifest, null, 2)}\n`);
}
