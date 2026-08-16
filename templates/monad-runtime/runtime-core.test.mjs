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
  calculateRuntimeVersion,
  loadRuntimeSource,
  normalizeApiUrl,
  validateTemplateRef,
} from './runtime-core.mjs';

test('runtime source pins immutable TAMS and tool inputs', async () => {
  const source = await loadRuntimeSource();
  assert.equal(
    source.tams_revision,
    'd3a3fa457c34841adca6996c1676556992e7908c',
  );
  assert.equal(
    source.tams_apps_sandbox_tree_oid,
    'd02032fa44e1834d1e84583a01e25701e1166cd4',
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
    assert.match(dockerfile, new RegExp(`monad-webtop-svc-${service}`));
    assert.match(
      dockerfile,
      new RegExp(`s6-overlay/s6-rc\\.d/svc-${service}/run`),
    );
  }
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
    /attestation\.daemon\.sha256!==sha256\("\/usr\/local\/bin\/monad-agent"\)/,
  );
  assert.match(
    dockerfile,
    /attestation\.admission_helper\.sha256!==sha256\("\/usr\/local\/libexec\/monad-tenant-admission"\)/,
  );
  assert.match(dockerfile, /execFileSync\("sha256sum"/);
  assert.match(dockerfile, /svc-monad-agent/);
  assert.doesNotMatch(dockerfile, /\b(?:kasm|novnc|tigervnc)\b/i);
});

test('daemon longrun establishes one exact real admission root before every exec', async () => {
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
  assert.ok(
    runScript.indexOf('0:0:700') <
      runScript.indexOf('exec /usr/local/bin/monad-entrypoint'),
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
  assert.equal(RUNTIME_BUILD_FILES.length, 5);
  assert.ok(
    RUNTIME_BUILD_FILES.some((file) => file.pathname.endsWith('/runtime-preflight.mjs')),
    'native runtime preflight code must contribute to the immutable digest',
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
    /exactProcRootFile\(daemonPid, "\/usr\/local\/bin\/monad-agent", 0o755\)/,
  );
  assert.match(
    verifier,
    /exactProcRootFile\(supervisorPid, "\/usr\/local\/libexec\/monad-webtop-svc-"/,
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
