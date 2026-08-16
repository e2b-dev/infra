import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { constants } from 'node:fs';
import { cp, lstat, mkdir, open, readFile, rm, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { calculateRuntimeVersion, loadRuntimeSource, requiredEnv } from './runtime-core.mjs';
import { runNativeAmd64RuntimePreflight } from './runtime-preflight.mjs';
import { buildTenantBoundaryAttestation } from './tenant-boundary.mjs';

const templateDir = fileURLToPath(new URL('.', import.meta.url));
const assetsDir = fileURLToPath(new URL('./.build-assets/', import.meta.url));
const serviceRunPath = fileURLToPath(
  new URL('./s6-overlay/s6-rc.d/svc-monad-agent/run', import.meta.url),
);

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

async function sha256FileHandle(handle) {
  const hash = createHash('sha256');
  const buffer = Buffer.allocUnsafe(64 * 1024);
  let position = 0;
  for (;;) {
    const { bytesRead } = await handle.read(buffer, 0, buffer.length, position);
    if (bytesRead === 0) break;
    hash.update(buffer.subarray(0, bytesRead));
    position += bytesRead;
  }
  return hash.digest('hex');
}

function sameFileMetadata(first, second) {
  return first.dev === second.dev &&
    first.ino === second.ino &&
    first.size === second.size &&
    first.mode === second.mode &&
    first.mtimeMs === second.mtimeMs &&
    first.ctimeMs === second.ctimeMs;
}

export async function attestPreparedExecutable(path) {
  const pathMetadata = await lstat(path);
  if (
    !pathMetadata.isFile() ||
    pathMetadata.isSymbolicLink() ||
    (pathMetadata.mode & 0o7777) !== 0o755
  ) {
    throw new Error(`prepared executable is not an exact regular no-follow file: ${path}`);
  }
  const handle = await open(
    path,
    constants.O_RDONLY | constants.O_CLOEXEC | constants.O_NOFOLLOW,
  );
  try {
    const metadata = await handle.stat();
    if (
      !metadata.isFile() ||
      metadata.dev !== pathMetadata.dev ||
      metadata.ino !== pathMetadata.ino ||
      metadata.size !== pathMetadata.size ||
      (metadata.mode & 0o7777) !== 0o755
    ) {
      throw new Error(`prepared executable is not an exact regular no-follow file: ${path}`);
    }
    const header = Buffer.alloc(20);
    const { bytesRead } = await handle.read(header, 0, header.length, 0);
    if (
      bytesRead !== header.length ||
      header.subarray(0, 4).toString('hex') !== '7f454c46' ||
      header.subarray(18, 20).toString('hex') !== '3e00'
    ) {
      throw new Error(`prepared executable is not Linux amd64 ELF: ${path}`);
    }
    const digest = await sha256FileHandle(handle);
    const finalMetadata = await handle.stat();
    let finalPathMetadata;
    try {
      finalPathMetadata = await lstat(path);
    } catch {
      throw new Error(`prepared executable changed during attestation: ${path}`);
    }
    if (
      !sameFileMetadata(metadata, finalMetadata) ||
      !sameFileMetadata(pathMetadata, finalPathMetadata) ||
      !sameFileMetadata(metadata, finalPathMetadata) ||
      !finalPathMetadata.isFile() ||
      finalPathMetadata.isSymbolicLink()
    ) {
      throw new Error(`prepared executable changed during attestation: ${path}`);
    }
    return digest;
  } finally {
    await handle.close();
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
  const helperContainer = `monad-runtime-helper-${shortVersion}`;
  const cliContainer = `monad-runtime-cli-${shortVersion}`;
  const bunContainer = `monad-runtime-bun-${shortVersion}`;

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
      agentImage,
      '/agent/dist/native/monad-tenant-admission',
      `${assetsDir}/monad-tenant-admission`,
      helperContainer,
    );
    await copyContainerFile(
      cliImage,
      '/cli/monad',
      `${assetsDir}/monad`,
      cliContainer,
    );
    await copyContainerFile(
      source.tool_versions.bun_base_image,
      '/usr/local/bin/bun',
      `${assetsDir}/bun`,
      bunContainer,
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

  const daemonSha256 = await attestPreparedExecutable(`${assetsDir}/monad-agent`);
  const admissionHelperSha256 = await attestPreparedExecutable(
    `${assetsDir}/monad-tenant-admission`,
  );
  const tenantBoundaryAttestation = buildTenantBoundaryAttestation({
    daemonSha256,
    admissionHelperSha256,
  });
  await writeFile(
    `${assetsDir}/session-rebind-tenant-boundary.json`,
    `${JSON.stringify(tenantBoundaryAttestation, null, 2)}\n`,
    { mode: 0o444 },
  );

  const nativeAmd64Preflight = await runNativeAmd64RuntimePreflight({
    assetsDir,
    serviceRunPath,
    baseImage: source.tool_versions.bun_base_image,
    runtimeVersion,
    daemonSha256,
    admissionHelperSha256,
  });

  const manifest = {
    prepared_at: new Date().toISOString(),
    tams_revision: revision,
    tams_apps_sandbox_tree_oid: appsSandboxTree,
    runtime_input_tree_oids: runtimeInputTreeOids,
    runtime_version: runtimeVersion,
    daemon_sha256: daemonSha256,
    tenant_admission_helper_sha256: admissionHelperSha256,
    native_amd64_preflight: nativeAmd64Preflight,
  };
  await writeFile(
    `${assetsDir}/manifest.json`,
    `${JSON.stringify(manifest, null, 2)}\n`,
  );

  const dockerfile = await readFile(`${templateDir}/e2b.Dockerfile`, 'utf8');
  if (/\b(?:kasm|novnc|tigervnc)\b/i.test(dockerfile)) {
    throw new Error('desktop Dockerfile must use Selkies, not a VNC stack');
  }
  if (
    !dockerfile.includes(source.tool_versions.webtop_image) ||
    !dockerfile.includes('CUSTOM_PORT=6080') ||
    !dockerfile.includes('CUSTOM_HTTPS_PORT=6081')
  ) {
    throw new Error('desktop Dockerfile does not match its pinned Webtop contract');
  }
  return manifest;
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const manifest = await prepareRuntimeAssets();
  process.stdout.write(`${JSON.stringify(manifest, null, 2)}\n`);
}
