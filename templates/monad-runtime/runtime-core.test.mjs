import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { Template } from 'e2b';
import { RUNTIME_BOOTSTRAP_READY_COMMAND } from './template.mjs';
import {
  bindTenantBoundaryMarker,
  classifyTenantBoundaryFilesystemStability,
  classifyTenantBoundaryProbeIdentity,
  classifyTenantBoundaryEvidence,
  classifyTenantBoundaryTopology,
  inspectTenantBoundaryMarkerFilesystem,
  tenantBoundaryProcRootPaths,
  tenantBoundaryRuntimePath,
  tenantBoundaryRuntimePathChain,
} from './runtime-verification-convergence.mjs';
import {
  parseNamespacePidVector,
  selectOuterPidForNamespacePid,
  verifyPinnedNginxProcesses,
  verifyPinnedWatchdogProcesses,
} from './runtime-process-contract.mjs';
import {
  S6_SERVICE_FILES,
  RUNTIME_BUILD_FILES,
  TENANT_SUPERVISED_SERVICES,
  assertCanonicalRuntimeTemplateRef,
  calculateRuntimeVersion,
  canonicalRuntimeTemplateRef,
  loadRuntimeSource,
  normalizeApiUrl,
  validateTemplateRef,
} from './runtime-core.mjs';

test('runtime template reference is canonically derived from the exact digest', () => {
  const runtimeVersion = '0123456789abcdef'.padEnd(64, '0');
  assert.equal(
    canonicalRuntimeTemplateRef(runtimeVersion),
    'monad-runtime:desktop-0123456789ab',
  );
  assert.equal(
    assertCanonicalRuntimeTemplateRef(
      'monad-runtime:desktop-0123456789ab',
      runtimeVersion,
    ),
    'monad-runtime:desktop-0123456789ab',
  );
  assert.throws(
    () => assertCanonicalRuntimeTemplateRef(
      'monad-runtime:desktop-deadbeefcafe',
      runtimeVersion,
    ),
    /must exactly match runtime version/,
  );
  for (const invalid of ['', 'abc', 'A'.repeat(64), '0'.repeat(63), '0'.repeat(65)]) {
    assert.throws(() => canonicalRuntimeTemplateRef(invalid));
  }
});

test('build rejects a noncanonical reference before any provider build', async () => {
  const build = await readFile(new URL('./build.mjs', import.meta.url), 'utf8');
  const create = build.indexOf('await createMonadRuntimeTemplate()');
  const canonical = build.indexOf(
    'assertCanonicalRuntimeTemplateRef(templateRef, runtimeVersion)',
  );
  const providerBuild = build.indexOf('await Template.build(');
  assert.ok(create >= 0);
  assert.ok(canonical > create);
  assert.ok(providerBuild > canonical);
  const verifier = await readFile(
    new URL('./verify-runtime.mjs', import.meta.url),
    'utf8',
  );
  assert.match(
    verifier,
    /assertCanonicalRuntimeTemplateRef\(templateRef, manifest\.runtime_version\)/,
  );
});

test('runtime source pins immutable TAMS and tool inputs', async () => {
  const source = await loadRuntimeSource();
  assert.equal(
    source.tams_revision,
    '9f4782f93613bfd50a28b81b496ea0a9b3a4266c',
  );
  assert.equal(
    source.tams_apps_sandbox_tree_oid,
    'f8b561c5efbd0b303e178d58e0608a184e8b7b13',
  );
  assert.equal(source.tool_versions.opencode, '1.14.28');
  assert.equal(source.tool_versions.agent_browser, '0.27.0');
  assert.equal(source.tool_versions.playwright, '1.60.0');
  assert.equal(
    source.tool_versions.remote_desktop,
    'LinuxServer Webtop Selkies on Ubuntu i3',
  );
  assert.match(
    source.tool_versions.webtop_image,
    /^lscr\.io\/linuxserver\/webtop@sha256:[0-9a-f]{64}$/,
  );
  assert.equal(source.template_resources.cpu_count, 2);
  assert.equal(source.template_resources.memory_mb, 4096);
});

test('Dockerfile preserves and gates every tenant-facing Webtop longrun', async () => {
  const dockerfile = await readFile(
    new URL('./e2b.Dockerfile', import.meta.url),
    'utf8',
  );
  const lastRuntimeBinMutation = dockerfile.indexOf(
    'COPY .build-assets/entrypoint.sh /opt/monad/runtime/bin/monad-entrypoint',
  );
  const canonicalBinMetadata = dockerfile.indexOf(
    'install -d -o root -g root -m 0755 /opt/monad/runtime/bin',
    lastRuntimeBinMutation,
  );
  assert.ok(lastRuntimeBinMutation >= 0, 'runtime entrypoint must be installed');
  assert.ok(
    canonicalBinMetadata > lastRuntimeBinMutation,
    'the dedicated runtime directory must be canonicalized after its final build-time mutation',
  );
  assert.match(
    dockerfile.slice(canonicalBinMetadata),
    /test -d \/opt\/monad\/runtime\/bin[\s\S]*test ! -L \/opt\/monad\/runtime\/bin[\s\S]*stat -c '%u:%g:%a' -- \/opt\/monad\/runtime\/bin[\s\S]*0:0:755/,
    'the final image must prove the dedicated runtime path is one real root:root 0755 directory',
  );
  for (const runtimeFile of ['monad-agent', 'monad-entrypoint']) {
    assert.match(
      dockerfile.slice(canonicalBinMetadata),
      new RegExp(
        `test -f /opt/monad/runtime/bin/${runtimeFile}` +
        `[\\s\\S]*test ! -L /opt/monad/runtime/bin/${runtimeFile}` +
        `[\\s\\S]*stat -c '%u:%g:%a' -- /opt/monad/runtime/bin/${runtimeFile}` +
        `[\\s\\S]*0:0:755`,
      ),
    );
  }
  const helperCopy = dockerfile.indexOf(
    'COPY .build-assets/monad-tenant-admission /opt/monad/runtime/libexec/monad-tenant-admission',
  );
  const canonicalHelperMetadata = dockerfile.indexOf(
    'for runtime_directory in /opt /opt/monad /opt/monad/runtime /opt/monad/runtime/libexec',
    helperCopy,
  );
  assert.ok(helperCopy >= 0, 'the admission helper must use the dedicated runtime path');
  assert.ok(
    canonicalHelperMetadata > helperCopy,
    'the helper directory must be canonicalized after its final build-time mutation',
  );
  assert.match(
    dockerfile.slice(canonicalHelperMetadata),
    /for runtime_directory in \/opt \/opt\/monad \/opt\/monad\/runtime \/opt\/monad\/runtime\/libexec[\s\S]*test -d "\$runtime_directory"[\s\S]*test ! -L "\$runtime_directory"[\s\S]*stat -c '%u:%g:%a' -- "\$runtime_directory"[\s\S]*0:0:755[\s\S]*test -f \/opt\/monad\/runtime\/libexec\/monad-tenant-admission[\s\S]*test ! -L \/opt\/monad\/runtime\/libexec\/monad-tenant-admission[\s\S]*0:0:755/,
  );
  assert.match(
    dockerfile,
    /COPY \.build-assets\/monad-agent \/opt\/monad\/runtime\/bin\/monad-agent/,
  );
  assert.match(
    dockerfile,
    /COPY \.build-assets\/entrypoint\.sh \/opt\/monad\/runtime\/bin\/monad-entrypoint/,
  );
  assert.match(
    dockerfile,
    /COPY \.build-assets\/monad \/usr\/local\/bin\/monad/,
    'the ordinary CLI must remain on the shared command path',
  );
  assert.doesNotMatch(dockerfile, /\/usr\/local\/bin\/monad-(?:agent|entrypoint)/);
  assert.match(dockerfile, /opencode-ai@1\.14\.28/);
  assert.match(dockerfile, /agent-browser@0\.27\.0/);
  assert.match(dockerfile, /playwright@1\.60\.0/);
  assert.match(dockerfile, /COPY \.build-assets\/monad-agent/);
  assert.match(dockerfile, /COPY \.build-assets\/monad-tenant-admission/);
  assert.match(
    dockerfile,
    /COPY \.build-assets\/session-rebind-tenant-boundary\.json/,
  );
  assert.deepEqual(TENANT_SUPERVISED_SERVICES, [
    'nginx',
    'xorg',
    'dbus',
    'pulseaudio',
    'selkies',
    'de',
    'watchdog',
    'xsettingsd',
  ]);
  for (const service of TENANT_SUPERVISED_SERVICES) {
    assert.match(
      dockerfile,
      new RegExp(`/opt/monad/runtime/libexec/monad-webtop-svc-${service}`),
    );
    assert.match(
      dockerfile,
      new RegExp(`s6-overlay/s6-rc\\.d/svc-${service}/run`),
    );
  }
  assert.doesNotMatch(dockerfile, /\/usr\/local\/libexec\/monad-webtop-svc-/);
  assert.doesNotMatch(dockerfile, /sed -i 's\/s6-setuidgid abc\/\/g'/);
  assert.doesNotMatch(dockerfile, /grep -Fq 's6-setuidgid abc'/);
  assert.match(dockerfile, /gpasswd -d abc sudo/);
  assert.match(dockerfile, /gpasswd -d abc docker/);
  assert.match(
    dockerfile,
    /abc_groups="\$\(id -G abc \| xargs -n1 \| sort -n \| paste -sd, -\)"/,
  );
  assert.match(dockerfile, /test "\$abc_groups" = "100,1001"/);
  assert.deepEqual(
    dockerfile.match(/ENV MONAD_TENANT_BOUNDARY_REQUIRED=1/g),
    ['ENV MONAD_TENANT_BOUNDARY_REQUIRED=1'],
    'the immutable runtime must fail closed when tenant-boundary setup is unavailable',
  );
  assert.doesNotMatch(dockerfile, /getent group (?:100|sudo|docker)/);
  assert.match(dockerfile, /svc-cron\/run/);
  assert.match(dockerfile, /exec sleep infinity/);
  assert.match(dockerfile, /Attested cron override: inactive/);
  assert.match(dockerfile, /chown -R 911:1001 \/opt\/monad\/home \/workspace/);
  assert.match(
    dockerfile,
    /FROM lscr\.io\/linuxserver\/webtop@sha256:[0-9a-f]{64}/,
  );
  assert.match(dockerfile, /CUSTOM_PORT=6080/);
  assert.match(dockerfile, /CUSTOM_HTTPS_PORT=6081/);
  assert.match(dockerfile, /NPM_CONFIG_CACHE=\/tmp\/npm-cache/);
  assert.match(dockerfile, /apt-get purge -y[\s\S]*docker-ce/);
  assert.match(dockerfile, /START_DOCKER=false/);
  assert.match(dockerfile, /RESTART_APP=false/);
  assert.match(
    dockerfile,
    /attestation\.daemon\.sha256!==sha256\("\/opt\/monad\/runtime\/bin\/monad-agent"\)/,
  );
  assert.match(
    dockerfile,
    /attestation\.admission_helper\.sha256!==sha256\("\/opt\/monad\/runtime\/libexec\/monad-tenant-admission"\)/,
  );
  assert.doesNotMatch(dockerfile, /\/usr\/local\/libexec\/monad-tenant-admission/);
  assert.match(dockerfile, /execFileSync\("sha256sum"/);
  assert.match(dockerfile, /svc-monad-agent/);
  assert.doesNotMatch(dockerfile, /\b(?:kasm|novnc|tigervnc)\b/i);
});

test('Dockerfile transports the digest-bound Selkies rewrite helper without inline escaping', async () => {
  const dockerfile = await readFile(
    new URL('./e2b.Dockerfile', import.meta.url),
    'utf8',
  );
  const rendered = Template.toDockerfile(Template().fromDockerfile(dockerfile));
  for (const value of [
    'COPY selkies-launcher-rewrite.mjs /tmp/monad-selkies-launcher-rewrite.mjs',
    'test -x /lsiopy/bin/selkies',
    'rm -f /tmp/monad-selkies-launcher-rewrite.mjs',
  ]) {
    assert.match(dockerfile, new RegExp(value.replaceAll('/', '\\/')));
    assert.match(rendered, new RegExp(value.replaceAll('/', '\\/')));
  }
  assert.match(
    dockerfile,
    /node \/tmp\/monad-selkies-launcher-rewrite\.mjs \\\n\s*\/opt\/monad\/runtime\/libexec\/monad-webtop-svc-selkies/,
  );
  assert.match(
    rendered,
    /node \/tmp\/monad-selkies-launcher-rewrite\.mjs \/opt\/monad\/runtime\/libexec\/monad-webtop-svc-selkies/,
  );
  const renderedStart = rendered.indexOf(
    'RUN test -x /lsiopy/bin/selkies',
  );
  const renderedEnd = rendered.indexOf('COPY s6-overlay/', renderedStart);
  assert.ok(renderedStart >= 0 && renderedEnd > renderedStart);
  const renderedRewrite = rendered.slice(renderedStart, renderedEnd);
  assert.doesNotMatch(renderedRewrite, /node -e|split\(|join\(|expected_before/);
});

test('daemon longrun verifies the immutable runtime path and establishes one exact admission root before every exec', async () => {
  const runScript = await readFile(
    new URL('./s6-overlay/s6-rc.d/svc-monad-agent/run', import.meta.url),
    'utf8',
  );
  assert.match(runScript, /^#!\/usr\/bin\/with-contenv bash\nset -euo pipefail\n/);
  assert.match(runScript, /admission_root=\/run\/monad-admission/);
  assert.match(runScript, /install -d -o root -g root -m 0700/);
  assert.match(runScript, /test -d "\$admission_root"/);
  assert.match(runScript, /test ! -L "\$admission_root"/);
  assert.match(runScript, /stat -c '%u:%g:%a' -- "\$admission_root"/);
  assert.match(runScript, /0:0:700/);
  assert.match(runScript, /runtime_bin=\/opt\/monad\/runtime\/bin/);
  assert.match(runScript, /test -d "\$runtime_bin"/);
  assert.match(runScript, /test ! -L "\$runtime_bin"/);
  assert.match(runScript, /stat -c '%u:%g:%a' -- "\$runtime_bin"/);
  assert.match(runScript, /entrypoint="\$runtime_bin\/monad-entrypoint"/);
  assert.match(runScript, /test -f "\$entrypoint"/);
  assert.match(runScript, /test ! -L "\$entrypoint"/);
  assert.match(runScript, /stat -c '%u:%g:%a' -- "\$entrypoint"/);
  assert.match(runScript, /runtime_libexec=\/opt\/monad\/runtime\/libexec/);
  assert.match(runScript, /helper="\$runtime_libexec\/monad-tenant-admission"/);
  assert.match(
    runScript,
    /for runtime_directory in \/opt \/opt\/monad \/opt\/monad\/runtime "\$runtime_libexec"/,
  );
  assert.match(runScript, /test -d "\$runtime_directory"/);
  assert.match(runScript, /test ! -L "\$runtime_directory"/);
  assert.match(runScript, /stat -c '%u:%g:%a' -- "\$runtime_directory"/);
  assert.match(runScript, /test -f "\$helper"/);
  assert.match(runScript, /test ! -L "\$helper"/);
  assert.match(runScript, /stat -c '%u:%g:%a' -- "\$helper"/);
  assert.doesNotMatch(
    runScript,
    /(?:install|chmod|chown)[^\n]*\$runtime_bin/,
    'runtime startup must fail closed instead of repairing immutable path metadata',
  );
  assert.ok(
    runScript.indexOf('0:0:700') <
      runScript.indexOf('exec "$entrypoint"'),
    'admission-root verification must precede daemon exec',
  );
});

test('E2B Dockerfile rendering preserves runtime hash and group verifiers', async () => {
  const dockerfile = await readFile(
    new URL('./e2b.Dockerfile', import.meta.url),
    'utf8',
  );
  const rendered = Template.toDockerfile(Template().fromDockerfile(dockerfile));
  const hashCheck = rendered
    .split('\n')
    .find((line) => line.includes('prepared daemon hash mismatch'));

  assert.ok(hashCheck, 'rendered Dockerfile must retain the runtime hash check');
  assert.match(hashCheck, /\.slice\(0,64\)/);
  assert.doesNotMatch(hashCheck, /\.split\(\/s\+\/\)/);
  assert.match(
    rendered,
    /abc_groups="\$\(id -G abc \| xargs -n1 \| sort -n \| paste -sd, -\)"/,
  );
  assert.match(rendered, /test "\$abc_groups" = "100,1001"/);
  assert.match(
    rendered,
    /install -d -o root -g root -m 0755 \/opt\/monad\/runtime\/bin/,
  );
  assert.match(
    rendered,
    /stat -c '%u:%g:%a' -- \/opt\/monad\/runtime\/bin\)" = "0:0:755"/,
  );
});

test('template build readiness proves bootstrap wait and delegates desktop proof', async () => {
  const definition = await readFile(
    new URL('./template.mjs', import.meta.url),
    'utf8',
  );
  // The daemon boots credential-gated (flag baked into the image), so
  // build-time readiness is "daemon awaiting bootstrap on its root-only
  // socket" — /monad/health cannot come up without minted session leases,
  // and the build/verify posture denies all egress anyway.
  assert.match(
    definition,
    /RUNTIME_BOOTSTRAP_READY_COMMAND/,
  );
  assert.match(
    RUNTIME_BOOTSTRAP_READY_COMMAND,
    /s6-svstat \/run\/service\/svc-monad-agent/,
  );
  assert.match(RUNTIME_BOOTSTRAP_READY_COMMAND, /pgrep -x monad-agent/);
  assert.doesNotMatch(RUNTIME_BOOTSTRAP_READY_COMMAND, /\/proc\/\$agent_pid/);
  assert.match(
    RUNTIME_BOOTSTRAP_READY_COMMAND,
    /test -S \/var\/run\/monad\/credential-bootstrap\.sock/,
  );
  assert.match(
    RUNTIME_BOOTSTRAP_READY_COMMAND,
    /test ! -L \/var\/run\/monad\/credential-bootstrap\.sock/,
  );
  assert.match(
    RUNTIME_BOOTSTRAP_READY_COMMAND,
    /stat -c %u:%g:%a \/var\/run\/monad\/credential-bootstrap\.sock/,
  );
  assert.match(RUNTIME_BOOTSTRAP_READY_COMMAND, /0:0:600/);
  assert.doesNotMatch(RUNTIME_BOOTSTRAP_READY_COMMAND, /8000\/monad\/health/);
  assert.doesNotMatch(RUNTIME_BOOTSTRAP_READY_COMMAND, /127\.0\.0\.1:6080/);
  assert.doesNotMatch(RUNTIME_BOOTSTRAP_READY_COMMAND, /127\.0\.0\.1:6081/);
  assert.match(
    definition,
    /unshare --pid --fork --mount-proc --kill-child=TERM \/init/,
  );
  assert.match(definition, /runtime-provenance\.json/);
  assert.doesNotMatch(definition, /\.setEnvs\(/);
  assert.doesNotMatch(definition, /8000\/health/);
  assert.equal(S6_SERVICE_FILES.length, 12);
  assert.equal(RUNTIME_BUILD_FILES.length, 6);
  assert.ok(
    RUNTIME_BUILD_FILES.some((file) => file.pathname.endsWith('/runtime-preflight.mjs')),
    'native runtime preflight code must contribute to the immutable digest',
  );
  assert.ok(
    RUNTIME_BUILD_FILES.some(
      (file) => file.pathname.endsWith('/selkies-launcher-rewrite.mjs'),
    ),
    'the transported Selkies rewrite helper must contribute to the immutable digest',
  );
  const serviceFiles = await Promise.all(
    S6_SERVICE_FILES.map((file) => readFile(file, 'utf8')),
  );
  assert.ok(serviceFiles.some((content) => content.includes('monad-entrypoint')));
  assert.ok(serviceFiles.some((content) => content.trim() === 'longrun'));
  for (const service of TENANT_SUPERVISED_SERVICES) {
    assert.ok(serviceFiles.some((content) => content.includes(
      `--supervised-service ${service}`,
    )));
  }
  assert.ok(serviceFiles.some((content) => content.includes(
    '/opt/monad/runtime/libexec/monad-tenant-admission',
  )));
  for (const content of serviceFiles) {
    assert.doesNotMatch(content, /\/usr\/local\/libexec\/monad-tenant-admission/);
  }
  assert.ok(serviceFiles.some((content) => content.includes('exec sleep infinity')));
  for (const content of serviceFiles) {
    assert.doesNotMatch(content, /--supervised-service \S+\s+\d/);
  }
});

test('asset preparation gates manifest acceptance on the native daemon preflight', async () => {
  const preparation = await readFile(
    new URL('./prepare-assets.mjs', import.meta.url),
    'utf8',
  );
  assert.match(preparation, /runNativeAmd64RuntimePreflight/);
  const preflight = preparation.indexOf('await runNativeAmd64RuntimePreflight(');
  const manifest = preparation.indexOf('const manifest = {');
  assert.ok(preflight >= 0 && manifest > preflight);
  for (const binding of [
    'assetsDir',
    'serviceRunPath',
    'baseImage: source.tool_versions.bun_base_image',
    'runtimeVersion',
    'daemonSha256',
    'entrypointSha256',
    'admissionHelperSha256',
  ]) {
    assert.ok(
      preparation.slice(preflight, manifest).includes(binding),
      `preflight must receive ${binding}`,
    );
  }

  const template = await readFile(
    new URL('./template.mjs', import.meta.url),
    'utf8',
  );
  assert.match(template, /validateNativeAmd64RuntimePreflightEvidence/);
  assert.match(template, /assetManifest\.native_amd64_preflight/);
  assert.match(template, /entrypointSha256: assetManifest\.entrypoint_sha256/);
  assert.match(template, /entrypoint_sha256: assetManifest\.entrypoint_sha256/);
  assert.match(preparation, /entrypoint_sha256: entrypointSha256/);
});

test('runtime verifier proves containment and disables ambient schedulers and nested Docker', async () => {
  const verifier = await readFile(
    new URL('./verify-runtime.mjs', import.meta.url),
    'utf8',
  );
  assert.match(verifier, /tenantBoundaryProcRootPaths/);
  assert.match(verifier, /session-rebind-tenant-boundary\.json/);
  for (const service of TENANT_SUPERVISED_SERVICES) {
    assert.match(verifier, new RegExp(`svc-${service}`));
  }
  assert.match(verifier, /monad-webtop-svc-/);
  assert.match(
    verifier,
    /exactProcRootFile\(supervisorPid, "\/opt\/monad\/runtime\/libexec\/monad-webtop-svc-"/,
  );
  assert.doesNotMatch(
    verifier,
    /exactProcRootFile\(supervisorPid, "\/usr\/local\/libexec\/monad-webtop-svc-"/,
  );
  assert.match(verifier, /root_daemon_outside_tenant_cgroup/);
  assert.match(verifier, /tenant_service_identity_match/);
  assert.match(verifier, /service_leader_cgroup_match/);
  assert.match(verifier, /important_descendant_cgroup_match/);
  assert.match(verifier, /cron_disabled/);
  assert.match(verifier, /dockerd_absent/);
  assert.match(verifier, /marker_basename_match/);
  assert.match(verifier, /marker_parent_daemon_cgroup_match/);
  assert.doesNotMatch(verifier, /marker_direct_parent_match/);
  assert.match(verifier, /s6-svstat/);
  assert.match(verifier, /selectOuterPidForNamespacePid/);
  assert.match(verifier, /parseNamespacePidVector\.toString\(\)/);
  assert.match(verifier, /selectOuterPidForNamespacePid\.toString\(\)/);
  assert.match(verifier, /service_up/);
  assert.match(verifier, /service_process_namespace_match/);
  assert.match(verifier, /service_state_stable/);
  assert.match(verifier, /supervisor_state_stable/);
  assert.match(verifier, /waitForTenantBoundaryEvidence/);
  assert.match(verifier, /probe_ok: false, stage: probeStage/);
  assert.match(verifier, /user === undefined \? \{\} : \{ user \}/);
  assert.match(verifier, /classifyBoundaryEvidence\(boundaryEvidence\)/);
  assert.match(verifier, /tenantBoundaryProcRootPaths/);
  assert.match(verifier, /supervisor_mount_namespace/);
  assert.match(verifier, /daemon_mount_namespace/);
  assert.match(verifier, /daemon_service_mapping/);
  assert.match(verifier, /daemon_supervisor_mount_namespace_match/);
  assert.match(verifier, /daemon_supervisor_root_identity_match/);
  assert.match(verifier, /daemon_supervisor_run_identity_match/);
  assert.match(verifier, /daemon_supervisor_filesystem_stable/);
  assert.match(verifier, /service_leader_mount_namespace_match/);
  assert.match(verifier, /daemonRuntimeRootPath/);
  assert.match(verifier, /procRootPathChain/);
  assert.match(verifier, /"\/proc\/" \+ daemonPid \+ "\/exe"/);
  assert.doesNotMatch(verifier, /sha256\("\/usr\/local\/bin\/monad-agent"\)/);
  assert.doesNotMatch(verifier, /JSON\.parse\(readFileSync\(attestationPath/);
  assert.match(verifier, /classifyBoundaryProbeIdentity/);
  assert.match(verifier, /inspectBoundaryMarkerFilesystem/);
  assert.doesNotMatch(verifier, /probeStage = "marker_directory"/);
  assert.doesNotMatch(verifier, /probeStage = "marker_file"/);
  assert.match(verifier, /probeStage = "marker_binding"/);
  assert.match(verifier, /probeStage = "marker_target"/);
  assert.doesNotMatch(verifier, /realpathSync\(tenantCgroup\)/);
  assert.doesNotMatch(verifier, /marker !== "\/sys\/fs\/cgroup\/monad-tenant\\n"/);
  assert.match(
    verifier,
    /bindBoundaryMarker\(cgroup\(daemonPid\), marker\)/,
  );
  assert.match(
    verifier,
    /exactProcRootDirectory\(daemonPid, tenantCgroup\)/,
  );
  assert.match(
    verifier,
    /exactProcRootDirectory\(supervisorPid, tenantCgroup\)/,
  );
  assert.match(
    verifier,
    /bindBoundaryMarker\(cgroup\(daemonPid\), finalMarker\)/,
  );
  assert.match(
    verifier,
    /attemptTimeoutMs,\s*attemptTimeoutMs,\s*'root'/,
  );
  assert.deepEqual(
    verifier.match(/'root'/g),
    ["'root'"],
    'only the tenant-boundary probe may opt into root execution',
  );
  for (const ordinaryProbe of [
    'bootstrapReadinessProbe',
    'browserProbe',
    'desktopProbe',
  ]) {
    assert.doesNotMatch(
      verifier,
      new RegExp(`\\$\\{${ordinaryProbe}\\}[^;]+['\"]root['\"]`),
    );
  }
  assert.match(verifier, /const daemonServiceState = serviceState\("monad-agent"\)/);
  assert.match(verifier, /const daemonMarkerPaths = procRootPaths\(daemonPid\)/);
  assert.match(verifier, /const supervisorMarkerPaths = procRootPaths\(supervisorPid\)/);
  assert.match(verifier, /authority: "daemon"/);
  assert.match(verifier, /authority: "supervisor"/);
  assert.match(verifier, /readFileSync\(daemonMarkerPaths\.marker, "utf8"\)/);
  assert.match(verifier, /readFileSync\(supervisorMarkerPaths\.marker, "utf8"\)/);
  assert.doesNotMatch(verifier, /const markerPaths = procRootPaths\(supervisorPid\)/);
  const daemonMarkerCheck = verifier.indexOf(
    'const daemonMarkerFilesystem = inspectBoundaryMarkerFilesystem',
  );
  const supervisorMarkerCheck = verifier.indexOf(
    'const supervisorMarkerFilesystem = inspectBoundaryMarkerFilesystem',
  );
  const markerContentRead = verifier.indexOf(
    'const marker = readFileSync(daemonMarkerPaths.marker, "utf8")',
  );
  assert.ok(
    daemonMarkerCheck >= 0 &&
    supervisorMarkerCheck > daemonMarkerCheck &&
    markerContentRead > supervisorMarkerCheck,
    'both marker views must pass in order before content is trusted',
  );
  assert.match(
    verifier.slice(supervisorMarkerCheck, markerContentRead),
    /if \(supervisorMarkerFilesystem\.probe_ok !== true\)[\s\S]*process\.exit\(0\)/,
  );
  assert.doesNotMatch(verifier, /daemonMarkerFilesystem\.probe_ok === true\s*\|\|/);
  assert.match(verifier, /exactProcRootFile\(daemonPid, attestationPath, 0o444\)/);
  assert.match(
    verifier,
    /exactProcRootDirectory\(daemonPid, "\/opt\/monad\/runtime\/bin", 0o755\)/,
  );
  assert.match(
    verifier,
    /exactProcRootFile\(daemonPid, "\/opt\/monad\/runtime\/bin\/monad-agent", 0o755\)/,
  );
  assert.match(
    verifier,
    /exactProcRootFile\(daemonPid, "\/opt\/monad\/runtime\/bin\/monad-entrypoint", 0o755\)/,
  );
  for (const directory of [
    '/opt',
    '/opt/monad',
    '/opt/monad/runtime',
    '/opt/monad/runtime/libexec',
  ]) {
    assert.ok(
      verifier.includes(`exactProcRootDirectory(daemonPid, "${directory}", 0o755)`),
      `the verifier must prove exact helper ancestor metadata for ${directory}`,
    );
  }
  assert.match(verifier, /entrypoint_sha256: entrypointSha256/);
  assert.match(
    verifier,
    /tenantBoundary\.entrypoint_sha256 !== manifest\.entrypoint_sha256/,
  );
  assert.doesNotMatch(verifier, /\/usr\/local\/bin\/monad-(?:agent|entrypoint)/);
  assert.match(
    verifier,
    /exactProcRootFile\(supervisorPid, "\/opt\/monad\/runtime\/libexec\/monad-webtop-svc-"/,
  );
  assert.doesNotMatch(
    verifier,
    /exactProcRootFile\(supervisorPid, attestationPath/,
  );
  assert.ok(
    verifier.indexOf('const finalDaemonRootIdentity = statSync') >
      verifier.indexOf('const markerExact ='),
    'final followed-root identity must be sampled after proc-root evidence',
  );
  assert.match(verifier, /const finalSupervisorRootIdentity = statSync/);
  assert.match(verifier, /const finalDaemonRunIdentity = statSync/);
  assert.match(verifier, /const finalSupervisorRunIdentity = statSync/);
  assert.match(verifier, /classifyBoundaryFilesystemStability\(\{/);
  assert.ok(
    verifier.indexOf('const finalSupervisorPid = uniqueNamedPid("s6-svscan")') >
      verifier.indexOf('const markerExact ='),
    'the supervisor identity must be rechecked after proc-root evidence reads',
  );
  assert.match(verifier, /expectedNamespace: supervisorNamespace/);
  assert.match(verifier, /expectedNamespaceDepth: supervisorNamespacePids\.length/);
  assert.match(verifier, /processes: namespaceProcesses/);
  assert.match(verifier, /unique_named_process/);
  assert.match(verifier, /socket_is_exact_type/);
  assert.match(verifier, /socket_uid !== 0/);
  assert.match(verifier, /socket_gid !== 0/);
  assert.match(verifier, /socket_mode !== '600'/);
  assert.match(verifier, /executable\(value\) === "\/usr\/sbin\/nginx"/);
  assert.match(verifier, /verifyPinnedNginxProcesses\.toString\(\)/);
  assert.match(verifier, /verifyPinnedWatchdogProcesses\.toString\(\)/);
  assert.match(verifier, /nginx_identity_match/);
  assert.match(verifier, /watchdog_identity_match/);
  assert.match(verifier, /JSON\.stringify\(attestation\.tenant\?\.services\)/);
  for (const service of ['chromium', 'git', 'opencode', 'selkies', 'xorg']) {
    assert.match(verifier, new RegExp(`${service}: expectedIdentity\\.uid`));
  }
  assert.doesNotMatch(verifier, /Object\.values\(attestation\.tenant\?\.services/);
});

test('runtime verifier renders standalone bootstrap and tenant probes as valid scripts', async () => {
  const verifier = await readFile(
    new URL('./verify-runtime.mjs', import.meta.url),
    'utf8',
  );
  const start = verifier.indexOf('const bootstrapReadinessProbe');
  const end = verifier.indexOf('let verificationError;');
  assert.ok(start >= 0 && end > start);
  const factory = new Function(
    'parseNamespacePidVector',
    'selectOuterPidForNamespacePid',
    'verifyPinnedNginxProcesses',
    'verifyPinnedWatchdogProcesses',
    'bindTenantBoundaryMarker',
    'classifyTenantBoundaryFilesystemStability',
    'classifyTenantBoundaryProbeIdentity',
    'classifyTenantBoundaryEvidence',
    'classifyTenantBoundaryTopology',
    'inspectTenantBoundaryMarkerFilesystem',
    'tenantBoundaryProcRootPaths',
    'tenantBoundaryRuntimePath',
    'tenantBoundaryRuntimePathChain',
    `${verifier.slice(start, end)}; return { bootstrapReadinessProbe, tenantBoundaryProbe };`,
  );
  const probes = factory(
    parseNamespacePidVector,
    selectOuterPidForNamespacePid,
    verifyPinnedNginxProcesses,
    verifyPinnedWatchdogProcesses,
    bindTenantBoundaryMarker,
    classifyTenantBoundaryFilesystemStability,
    classifyTenantBoundaryProbeIdentity,
    classifyTenantBoundaryEvidence,
    classifyTenantBoundaryTopology,
    inspectTenantBoundaryMarkerFilesystem,
    tenantBoundaryProcRootPaths,
    tenantBoundaryRuntimePath,
    tenantBoundaryRuntimePathChain,
  );
  for (const encoded of Object.values(probes)) {
    const script = Buffer.from(encoded, 'base64').toString('utf8');
    assert.doesNotThrow(() => new Function(script));
  }

  const tenantScript = Buffer.from(
    probes.tenantBoundaryProbe,
    'base64',
  ).toString('utf8');
  const filterStart = tenantScript.indexOf(
    'const filterNamespaceProcesses =',
  );
  const filterEnd = tenantScript.indexOf(
    ';\nconst namespaceProcessEvidence',
    filterStart,
  );
  assert.ok(
    filterStart >= 0 && filterEnd > filterStart,
    'the rendered verifier must filter namespace inventory before selection',
  );
  const filterNamespaceProcesses = new Function(
    `${tenantScript.slice(filterStart, filterEnd + 1)}; return filterNamespaceProcesses;`,
  )();
  const common = {
    namespace: 'pid:[4026533000]',
    mountNamespace: 'mnt:[4026533001]',
    cgroup: '0::/daemon/monad-tenant',
  };
  const processes = filterNamespaceProcesses([
    { pid: 1, namespacePids: [1], ...common },
    { pid: 82, namespacePids: [82, 17], ...common },
  ]);
  assert.deepEqual(processes.map(({ pid }) => pid), [82]);
  assert.equal(selectOuterPidForNamespacePid({
    innerPid: 17,
    expectedMembership: common.cgroup,
    expectedNamespace: common.namespace,
    expectedNamespaceDepth: 2,
    processes,
  }), 82);
  const malformed = filterNamespaceProcesses([
    { pid: 91, namespacePids: [91, 0], ...common },
  ]);
  assert.throws(() => selectOuterPidForNamespacePid({
    innerPid: 17,
    expectedMembership: common.cgroup,
    expectedNamespace: common.namespace,
    expectedNamespaceDepth: 2,
    processes: malformed,
  }), /namespace PID process evidence is invalid/);
  assert.match(tenantScript, /marker_\$\{authority\}_\$\{target\}_\$\{category\}/);
  assert.match(tenantScript, /code === 'ENOENT'/);
  assert.match(tenantScript, /code === 'EACCES' \|\| code === 'EPERM'/);
  for (const stage of [
    'daemon_service_mapping',
    'daemon_supervisor_mount_namespace',
    'daemon_supervisor_root_identity',
    'daemon_supervisor_run_identity',
  ]) {
    assert.match(tenantScript, new RegExp(stage));
  }
  const helperStart = tenantScript.indexOf('const exactProcRootFile');
  const helperEnd = tenantScript.indexOf('let probeStage');
  assert.ok(helperStart >= 0 && helperEnd > helperStart);
  const helperFactory = new Function(
    'lstatSync',
    'procRootPathChain',
    `${tenantScript.slice(helperStart, helperEnd)}; return { exactProcRootFile, exactProcRootDirectory };`,
  );
  const observedPids = [];
  const helpers = helperFactory(
    (path) => ({
      isDirectory: () => !path.endsWith('tenant-cgroup-ready'),
      isFile: () => path.endsWith('tenant-cgroup-ready'),
      isSymbolicLink: () => false,
      uid: 0,
      gid: 0,
      mode: path.endsWith('tenant-cgroup-ready') ? 0o444 : 0o700,
    }),
    (supervisorPid, runtimePath) => {
      observedPids.push(supervisorPid);
      return runtimePath === '/run/monad-admission/tenant-cgroup-ready'
        ? ['/proc/1006/root/run', '/proc/1006/root/run/monad-admission',
          '/proc/1006/root/run/monad-admission/tenant-cgroup-ready']
        : ['/proc/1006/root/run', '/proc/1006/root/run/monad-admission'];
    },
  );
  assert.equal(
    helpers.exactProcRootFile(
      1006,
      '/run/monad-admission/tenant-cgroup-ready',
      0o444,
    ),
    true,
  );
  assert.equal(
    helpers.exactProcRootDirectory(1006, '/run/monad-admission', 0o700),
    true,
  );
  assert.deepEqual(observedPids, [1006, 1006]);
});

test('runtime version changes with tree identity', async () => {
  const source = await loadRuntimeSource();
  const first = await calculateRuntimeVersion({
    ...source,
    runtime_input_tree_oids: { 'apps/sandbox': 'a'.repeat(40) },
  });
  const second = await calculateRuntimeVersion({
    ...source,
    runtime_input_tree_oids: { 'apps/sandbox': 'b'.repeat(40) },
  });
  assert.match(first, /^[0-9a-f]{64}$/);
  assert.notEqual(first, second);
});

test('template reference is immutable and correctly named', () => {
  assert.equal(
    validateTemplateRef('monad-runtime:base-1234abcd'),
    'monad-runtime:base-1234abcd',
  );
  assert.throws(() => validateTemplateRef('monad-runtime:latest'));
  assert.throws(() => validateTemplateRef('other-runtime:v1'));
});

test('API URL must be credential-free HTTPS', () => {
  assert.equal(
    normalizeApiUrl('https://api.e2b.monad0.net/'),
    'https://api.e2b.monad0.net',
  );
  assert.throws(() => normalizeApiUrl('http://api.e2b.monad0.net'));
  assert.throws(() => normalizeApiUrl('https://user:pass@example.test'));
});
