import assert from 'node:assert/strict';
import test from 'node:test';

import {
  runNativeAmd64RuntimePreflight,
  validateNativeAmd64RuntimePreflightEvidence,
} from './runtime-preflight.mjs';

const inputs = Object.freeze({
  assetsDir: '/tmp/monad assets',
  serviceRunPath: '/repo/templates/monad-runtime/s6-overlay/s6-rc.d/svc-monad-agent/run',
  baseImage: 'oven/bun:1-debian@sha256:' + 'a'.repeat(64),
  runtimeVersion: 'b'.repeat(64),
  daemonSha256: 'c'.repeat(64),
  admissionHelperSha256: 'd'.repeat(64),
  containerName: 'monad-runtime-preflight-bbbbbbbbbbbb',
});

function commandKey(args) {
  return args[0] === 'container' ? `${args[0]} ${args[1]}` : args[0];
}

test('runs the exact daemon credential-free in one native private cgroup and cleans it', async () => {
  const calls = [];
  const runDocker = (args) => {
    calls.push(args);
    switch (commandKey(args)) {
      case 'info': return 'linux/x86_64';
      case 'create': return 'preflight-container-id';
      case 'start': return inputs.containerName;
      case 'exec': {
        const probe = args.at(-1);
        assert.doesNotMatch(
          probe,
          /\/proc\/1\/exe/,
          'a compiled launcher may intentionally differ from the proc executable',
        );
        return '/sys/fs/cgroup/monad-tenant';
      }
      case 'container rm': return inputs.containerName;
      default: throw new Error(`unexpected docker command: ${args.join(' ')}`);
    }
  };

  const evidence = await runNativeAmd64RuntimePreflight({
    ...inputs,
    runDocker,
    sleep: async () => {},
  });

  assert.deepEqual(evidence, {
    platform: 'linux/amd64',
    cgroup_namespace: 'private',
    network: 'none',
    admission_root_mode: '700',
    marker_mode: '444',
    marker_path: '/sys/fs/cgroup/monad-tenant',
    bootstrap_socket_mode: '600',
    credential_bootstrap: 'awaiting',
    daemon_sha256: inputs.daemonSha256,
    tenant_admission_helper_sha256: inputs.admissionHelperSha256,
  });

  const create = calls.find((args) => args[0] === 'create');
  assert.ok(create);
  const serializedCreate = create.join('\n');
  for (const required of [
    '--platform', 'linux/amd64',
    '--privileged',
    '--cgroupns', 'private',
    '--network', 'none',
    '--user', 'root',
    'MONAD_TENANT_BOUNDARY_REQUIRED=1',
    'MONAD_CREDENTIAL_BOOTSTRAP_REQUIRED=1',
  ]) {
    assert.ok(create.includes(required), `missing docker create argument ${required}`);
  }
  assert.deepEqual(
    create.flatMap((value, index) => value === '--env' ? [create[index + 1]] : []),
    [
      'MONAD_TENANT_BOUNDARY_REQUIRED=1',
      'MONAD_CREDENTIAL_BOOTSTRAP_REQUIRED=1',
      'MONAD_WORKSPACE=/workspace',
    ],
    'the credential-free preflight must not inherit or inject a credential',
  );
  assert.ok(serializedCreate.includes(
    `source=${inputs.assetsDir},target=/opt/monad-preflight/assets,readonly`,
  ));
  assert.ok(serializedCreate.includes(inputs.serviceRunPath));
  assert.match(serializedCreate, /\/opt\/monad-preflight\/svc-monad-agent-run/);
  for (const install of [
    'install -o root -g root -m 0755 /opt/monad-preflight/assets/monad-agent /usr/local/bin/monad-agent',
    'install -o root -g root -m 0755 /opt/monad-preflight/assets/monad-tenant-admission /usr/local/libexec/monad-tenant-admission',
    'install -o root -g root -m 0444 /opt/monad-preflight/assets/session-rebind-tenant-boundary.json /etc/monad/session-rebind-tenant-boundary.json',
    'install -o root -g root -m 0755 /opt/monad-preflight/assets/entrypoint.sh /usr/local/bin/monad-entrypoint',
  ]) {
    assert.ok(serializedCreate.includes(install), `missing canonical install: ${install}`);
  }
  const probe = calls.find((args) => args[0] === 'exec');
  const serializedProbe = probe.join('\n');
  assert.match(serializedProbe, /\/proc\/1\/cmdline/);
  assert.match(serializedProbe, /read -r -d '' first_argv/);
  assert.match(serializedProbe, /\/usr\/local\/bin\/monad-agent/);
  assert.doesNotMatch(serializedProbe, /\/proc\/1\/exe/);
  assert.match(serializedProbe, /sha256sum "\$launcher"/);
  assert.ok(serializedProbe.includes(inputs.daemonSha256));
  assert.ok(serializedProbe.includes(inputs.admissionHelperSha256));
  assert.match(serializedProbe, /sha256sum \/usr\/local\/libexec\/monad-tenant-admission/);
  assert.match(serializedProbe, /test ! -L "\$helper"/);
  assert.match(serializedProbe, /0:0:700/);
  assert.match(serializedProbe, /0:0:444/);
  assert.match(serializedProbe, /0:0:600/);
  assert.match(serializedProbe, /od -An -tx1/);
  assert.equal(calls.filter((args) => commandKey(args) === 'container rm').length, 1);
});

test('rejects a non-native Docker host before creating a privileged container', async () => {
  const calls = [];
  await assert.rejects(runNativeAmd64RuntimePreflight({
    ...inputs,
    runDocker: (args) => {
      calls.push(args);
      return 'linux/aarch64';
    },
  }), /native linux\/amd64 Docker host/);
  assert.deepEqual(calls.map(commandKey), ['info']);
});

test('cleans only a container this invocation created when evidence never appears', async () => {
  const calls = [];
  let now = 0;
  const runDocker = (args) => {
    calls.push(args);
    switch (commandKey(args)) {
      case 'info': return 'linux/amd64';
      case 'create': return 'preflight-container-id';
      case 'start': return inputs.containerName;
      case 'exec': throw new Error('private daemon detail');
      case 'container inspect': return 'true';
      case 'container rm': return inputs.containerName;
      default: throw new Error(`unexpected docker command: ${args.join(' ')}`);
    }
  };
  await assert.rejects(runNativeAmd64RuntimePreflight({
    ...inputs,
    runDocker,
    now: () => now,
    sleep: async (milliseconds) => { now += milliseconds; },
    timeoutMs: 2_000,
    intervalMs: 1_000,
  }), /did not publish exact credential-free evidence/);
  assert.equal(calls.filter((args) => commandKey(args) === 'container rm').length, 1);
});

test('does not remove an unowned colliding container when create fails', async () => {
  const calls = [];
  await assert.rejects(runNativeAmd64RuntimePreflight({
    ...inputs,
    runDocker: (args) => {
      calls.push(args);
      if (args[0] === 'info') return 'linux/x86_64';
      throw new Error('name already in use');
    },
  }), /name already in use/);
  assert.equal(calls.filter((args) => commandKey(args) === 'container rm').length, 0);
});

test('rejects noncanonical marker evidence and still cleans the container', async () => {
  const calls = [];
  await assert.rejects(runNativeAmd64RuntimePreflight({
    ...inputs,
    runDocker: (args) => {
      calls.push(args);
      switch (commandKey(args)) {
        case 'info': return 'linux/x86_64';
        case 'create': return 'preflight-container-id';
        case 'start': return inputs.containerName;
        case 'exec': return '/sys/fs/cgroup/../escape/monad-tenant';
        case 'container rm': return inputs.containerName;
        default: throw new Error(`unexpected docker command: ${args.join(' ')}`);
      }
    },
    sleep: async () => {},
  }), /invalid marker evidence/);
  assert.equal(calls.filter((args) => commandKey(args) === 'container rm').length, 1);
});

test('accepts only closed preflight evidence bound to both prepared executables', () => {
  const evidence = {
    platform: 'linux/amd64',
    cgroup_namespace: 'private',
    network: 'none',
    admission_root_mode: '700',
    marker_mode: '444',
    marker_path: '/sys/fs/cgroup/user/monad-tenant',
    bootstrap_socket_mode: '600',
    credential_bootstrap: 'awaiting',
    daemon_sha256: inputs.daemonSha256,
    tenant_admission_helper_sha256: inputs.admissionHelperSha256,
  };
  assert.deepEqual(validateNativeAmd64RuntimePreflightEvidence(evidence, {
    daemonSha256: inputs.daemonSha256,
    admissionHelperSha256: inputs.admissionHelperSha256,
  }), evidence);
  for (const invalid of [
    { ...evidence, platform: 'linux/arm64' },
    { ...evidence, network: 'bridge' },
    { ...evidence, marker_path: '/sys/fs/cgroup/../monad-tenant' },
    { ...evidence, daemon_sha256: 'e'.repeat(64) },
    { ...evidence, tenant_admission_helper_sha256: 'e'.repeat(64) },
    { ...evidence, diagnostic: 'expanded record' },
  ]) {
    assert.throws(
      () => validateNativeAmd64RuntimePreflightEvidence(invalid, {
        daemonSha256: inputs.daemonSha256,
        admissionHelperSha256: inputs.admissionHelperSha256,
      }),
      /native amd64 runtime preflight evidence is invalid/,
    );
  }
});
