import { access, readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { Template } from 'e2b';
import { calculateRuntimeVersion, loadRuntimeSource } from './runtime-core.mjs';
import { validateNativeAmd64RuntimePreflightEvidence } from './runtime-preflight.mjs';

const templateDir = fileURLToPath(new URL('.', import.meta.url));
const assetManifestPath = fileURLToPath(
  new URL('./.build-assets/manifest.json', import.meta.url),
);

export const RUNTIME_BOOTSTRAP_READY_COMMAND =
  "s6-svstat /run/service/svc-monad-agent | grep -q '^up '" +
  ' && agent_pid="$(pgrep -x monad-agent)"' +
  ' && test "$agent_pid" -gt 1' +
  ' && test -S /var/run/monad/credential-bootstrap.sock' +
  ' && test ! -L /var/run/monad/credential-bootstrap.sock' +
  ' && test "$(stat -c %u:%g:%a /var/run/monad/credential-bootstrap.sock)" = 0:0:600';

export async function createMonadRuntimeTemplate() {
  await access(assetManifestPath);
  const source = await loadRuntimeSource();
  const assetManifest = JSON.parse(await readFile(assetManifestPath, 'utf8'));
  if (assetManifest.tams_revision !== source.tams_revision) {
    throw new Error('prepared assets do not match the pinned TAMS revision');
  }
  validateNativeAmd64RuntimePreflightEvidence(
    assetManifest.native_amd64_preflight,
    {
      daemonSha256: assetManifest.daemon_sha256,
      admissionHelperSha256: assetManifest.tenant_admission_helper_sha256,
    },
  );

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
        // The daemon boots credential-gated (MONAD_CREDENTIAL_BOOTSTRAP_REQUIRED
        // is baked into the image) and cannot serve /monad/health until a
        // session bootstrap arrives — which never happens at build time, and
        // could not mint anyway under the build/verify deny-all egress posture.
        // Build-time readiness is therefore only the supervised daemon waiting
        // on its exact root-only bootstrap socket. The post-build verifier is
        // the authority for the marker-gated desktop/cgroup surface, and the
        // credentialed canary remains the authority for session runtime health.
        RUNTIME_BOOTSTRAP_READY_COMMAND,
      );
    return { template, runtimeVersion, source, assetManifest };
  } finally {
    process.chdir(previousCwd);
  }
}
