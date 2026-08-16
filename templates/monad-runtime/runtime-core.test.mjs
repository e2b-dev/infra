import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { Template } from 'e2b';
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
  assert.match(source.tams_revision, /^[0-9a-f]{40}$/);
  assert.match(source.tams_apps_sandbox_tree_oid, /^[0-9a-f]{40}$/);
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
  assert.match(dockerfile, /test "\$\(id -G abc\)" = "1001 100"/);
  assert.match(dockerfile, /grep -Eq '\(\^\|\[:,\]\)abc\(,\|\$\)'/);
  assert.match(dockerfile, /svc-cron\/run/);
  assert.match(dockerfile, /exec sleep infinity/);
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

test('E2B Dockerfile rendering preserves the runtime hash verifier', async () => {
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
});

test('template readiness composes bootstrap-await and desktop health under s6', async () => {
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
    /test -S \/var\/run\/monad\/credential-bootstrap\.sock/,
  );
  assert.doesNotMatch(definition, /8000\/monad\/health/);
  assert.match(definition, /127\.0\.0\.1:6080/);
  assert.match(definition, /127\.0\.0\.1:6081/);
  assert.match(
    definition,
    /unshare --pid --fork --mount-proc --kill-child=TERM \/init/,
  );
  assert.match(definition, /runtime-provenance\.json/);
  assert.doesNotMatch(definition, /\.setEnvs\(/);
  assert.doesNotMatch(definition, /8000\/health/);
  assert.equal(S6_SERVICE_FILES.length, 12);
  assert.equal(RUNTIME_BUILD_FILES.length, 4);
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

test('runtime verifier proves containment and disables ambient schedulers and nested Docker', async () => {
  const verifier = await readFile(
    new URL('./verify-runtime.mjs', import.meta.url),
    'utf8',
  );
  assert.match(verifier, /tenant-cgroup-ready/);
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
  assert.match(verifier, /marker_direct_parent_match/);
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
