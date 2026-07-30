import { access, readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { Template } from 'e2b';
import { calculateRuntimeVersion, loadRuntimeSource } from './runtime-core.mjs';

const templateDir = fileURLToPath(new URL('.', import.meta.url));
const assetManifestPath = fileURLToPath(
  new URL('./.build-assets/manifest.json', import.meta.url),
);

export async function createMonadRuntimeTemplate() {
  await access(assetManifestPath);
  const source = await loadRuntimeSource();
  const assetManifest = JSON.parse(await readFile(assetManifestPath, 'utf8'));
  if (assetManifest.tams_revision !== source.tams_revision) {
    throw new Error('prepared assets do not match the pinned TAMS revision');
  }

  const sourceWithTrees = {
    ...source,
    runtime_input_tree_oids: assetManifest.runtime_input_tree_oids,
  };
  const runtimeVersion = await calculateRuntimeVersion(sourceWithTrees);
  if (runtimeVersion !== assetManifest.runtime_version) {
    throw new Error('prepared asset manifest has an invalid runtime version');
  }

  const previousCwd = process.cwd();
  process.chdir(templateDir);
  try {
    const provenance = JSON.stringify({
      runtime_version: runtimeVersion,
      tams_revision: source.tams_revision,
      tams_apps_sandbox_tree_oid: source.tams_apps_sandbox_tree_oid,
      runtime_input_tree_oids: assetManifest.runtime_input_tree_oids,
    });
    const provenanceBase64 = Buffer.from(provenance).toString('base64');
    const template = Template()
      .fromDockerfile('e2b.Dockerfile')
      .runCmd(
        `install -d -m 0755 /opt/monad && printf '%s' '${provenanceBase64}' | base64 -d > /opt/monad/runtime-provenance.json && chmod 0444 /opt/monad/runtime-provenance.json`,
        { user: 'root' },
      )
      .setStartCmd(
        'unshare --pid --fork --mount-proc --kill-child=TERM /init',
        "curl -fsS http://127.0.0.1:8000/monad/health | jq -e '.daemon == \"ok\" and .opencode == \"ok\" and .runtimeReady == true' >/dev/null && curl -fsS http://127.0.0.1:6080/ >/dev/null && curl -fkSs https://127.0.0.1:6081/ >/dev/null",
      );
    return { template, runtimeVersion, source, assetManifest };
  } finally {
    process.chdir(previousCwd);
  }
}
