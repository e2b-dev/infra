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
      case 'create': {
        const imageIndex = args.indexOf(inputs.baseImage);
        const entrypointIndex = args.indexOf('--entrypoint');
        const effectiveEntrypoint = entrypointIndex === -1
          ? '/usr/local/bin/docker-entrypoint.sh'
          : args[entrypointIndex + 1];
        assert.deepEqual(
          [effectiveEntrypoint, ...args.slice(imageIndex + 1, imageIndex + 3)],
          ['/bin/bash', '-ceu', args.at(-1)],
          'the pinned Bun entrypoint must be replaced by the preflight shell',
        );
        return 'preflight-container-id';
      }
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
  const entrypointIndex = create.indexOf('--entrypoint');
  const imageIndex = create.indexOf(inputs.baseImage);
  assert.ok(entrypointIndex >= 0);
  assert.equal(create[entrypointIndex + 1], '/bin/bash');
  assert.equal(imageIndex, entrypointIndex + 2);
  assert.deepEqual(create.slice(imageIndex + 1, -1), ['-ceu']);
  assert.match(create.at(-1), /^install -d [\s\S]*exec \/bin\/bash \/opt\/monad-preflight\/svc-monad-agent-run$/);
  for (const required of [
    '--platform', 'linux/amd64',
    '--init',
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
    'Docker must receive only the three fixed non-secret environment values',
  );
  assert.doesNotMatch(serializedCreate, /E2B_API_KEY|MONAD_CREDENTIAL_LEASE_BUNDLE|--env-file/);
  assert.deepEqual(
    create.flatMap((value, index) => value === '--mount' ? [create[index + 1]] : []),
    [
      `type=bind,source=${inputs.assetsDir},target=/opt/monad-preflight/assets,readonly`,
      `type=bind,source=${inputs.serviceRunPath},target=/opt/monad-preflight/svc-monad-agent-run,readonly`,
    ],
    'the credential-free preflight must mount only readonly prepared build inputs',
  );
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
  assert.match(serializedProbe, /\/proc\/\$daemon_pid\/cmdline/);
  assert.match(serializedProbe, /\/proc\/\[0-9\]\*\/comm/);
  assert.match(serializedProbe, /test "\$daemon_pid" -gt 1/);
  assert.doesNotMatch(serializedProbe, /\/proc\/1\/(?:cmdline|cgroup)/);
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
  let logReads = 0;
  let now = 0;
  const runDocker = (args) => {
    calls.push(args);
    switch (commandKey(args)) {
      case 'info': return 'linux/amd64';
      case 'create': return 'preflight-container-id';
      case 'start': return inputs.containerName;
      case 'exec': throw new Error('private daemon detail');
      case 'container inspect': return 'true 0';
      case 'container rm': return inputs.containerName;
      default: throw new Error(`unexpected docker command: ${args.join(' ')}`);
    }
  };
  await assert.rejects(runNativeAmd64RuntimePreflight({
    ...inputs,
    runDocker,
    readContainerLogs: () => { logReads += 1; return ''; },
    now: () => now,
    sleep: async (milliseconds) => { now += milliseconds; },
    timeoutMs: 2_000,
    intervalMs: 1_000,
  }), /did not publish exact credential-free evidence/);
  assert.equal(logReads, 0, 'a running container must never trigger log collection');
  assert.equal(calls.filter((args) => commandKey(args) === 'container rm').length, 1);
});

test('reports only bounded structured boot-fatal evidence when the daemon exits', async () => {
  const calls = [];
  const rawSecret = 'MONAD_CREDENTIAL_LEASE_BUNDLE=must-not-leak';
  const longMessage = `tenant cgroup unavailable\n\u009b\u202e${'x'.repeat(400)}\u0000trailing`;
  const expectedMessage = `tenant cgroup unavailable ${'x'.repeat(214)}`;
  const runDocker = (args) => {
    calls.push(args);
    switch (commandKey(args)) {
      case 'info': return 'linux/amd64';
      case 'create': return 'preflight-container-id';
      case 'start': return inputs.containerName;
      case 'exec': throw new Error(rawSecret);
      case 'container inspect': return 'false 23';
      case 'container rm': return inputs.containerName;
      default: throw new Error(`unexpected docker command: ${args.join(' ')}`);
    }
  };

  await assert.rejects(runNativeAmd64RuntimePreflight({
    ...inputs,
    runDocker,
    readContainerLogs: () => [
      JSON.stringify({ t: 'now', level: 'info', msg: '[boot] starting', rawSecret }),
      JSON.stringify({
        t: 'now',
        level: 'error',
        msg: '[boot] fatal',
        error: { name: 'Error', message: longMessage, stack: rawSecret },
      }),
      rawSecret,
    ].join('\n'),
    sleep: async () => {},
  }), (error) => {
    assert.equal(error.name, 'Error');
    const diagnostic = JSON.parse(error.message.slice(error.message.indexOf('{')));
    assert.deepEqual(diagnostic, {
      exit_code: 23,
      fatal: { name: 'Error', message: expectedMessage },
    });
    assert.doesNotMatch(error.message, /trailing/);
    assert.ok(error.message.length < 500, 'diagnostic must remain bounded');
    assert.doesNotMatch(error.message, /MONAD_CREDENTIAL_LEASE_BUNDLE|must-not-leak|stack/);
    return true;
  });
  assert.equal(calls.filter((args) => commandKey(args) === 'container rm').length, 1);
});

test('redacts an untrusted error name but preserves its single sanitized message', async () => {
  const secret = 'ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789';
  await assert.rejects(runNativeAmd64RuntimePreflight({
    ...inputs,
    runDocker: (args) => {
      switch (commandKey(args)) {
        case 'info': return 'linux/amd64';
        case 'create': return 'preflight-container-id';
        case 'start': return inputs.containerName;
        case 'exec': throw new Error('probe failed');
        case 'container inspect': return 'false 1';
        case 'container rm': return inputs.containerName;
        default: throw new Error(`unexpected docker command: ${args.join(' ')}`);
      }
    },
    readContainerLogs: () => JSON.stringify({
      level: 'error',
      msg: '[boot] fatal',
      error: { name: secret, message: 'deterministic daemon failure\nsubstage' },
    }),
    sleep: async () => {},
  }), (error) => {
    assert.match(error.message, /"exit_code":1/);
    assert.match(error.message, /"name":"\[redacted\]"/);
    assert.match(error.message, /"message":"deterministic daemon failure substage"/);
    assert.doesNotMatch(error.message, new RegExp(secret));
    return true;
  });
});

test('reports one sanitized bounded system boot failure message', async () => {
  const privatePath = '/private/runtime/path';
  const privateErrno = 'EPRIVATELEAK';
  await assert.rejects(runNativeAmd64RuntimePreflight({
    ...inputs,
    runDocker: (args) => {
      switch (commandKey(args)) {
        case 'info': return 'linux/amd64';
        case 'create': return 'preflight-container-id';
        case 'start': return inputs.containerName;
        case 'exec': throw new Error('probe failed');
        case 'container inspect': return 'false 1';
        case 'container rm': return inputs.containerName;
        default: throw new Error(`unexpected docker command: ${args.join(' ')}`);
      }
    },
    readContainerLogs: () => JSON.stringify({
      level: 'error',
      msg: '[boot] fatal',
      error: { name: 'Error', message: `${privateErrno}: permission denied, mkdir '${privatePath}'` },
    }),
    sleep: async () => {},
  }), (error) => {
    assert.match(
      error.message,
      /"message":"EPRIVATELEAK: permission denied, mkdir '\/private\/runtime\/path'"/,
    );
    return true;
  });
});

test('distinguishes exact tenant-boundary attestation policy failures', async () => {
  for (const message of [
    'tenant_boundary_attestation_missing',
    'tenant_boundary_attestation_invalid',
  ]) {
    await assert.rejects(runNativeAmd64RuntimePreflight({
      ...inputs,
      runDocker: (args) => {
        switch (commandKey(args)) {
          case 'info': return 'linux/amd64';
          case 'create': return 'preflight-container-id';
          case 'start': return inputs.containerName;
          case 'exec': throw new Error('probe failed');
          case 'container inspect': return 'false 1';
          case 'container rm': return inputs.containerName;
          default: throw new Error(`unexpected docker command: ${args.join(' ')}`);
        }
      },
      readContainerLogs: () => JSON.stringify({
        level: 'error',
        msg: '[boot] fatal',
        error: { name: 'Error', message },
      }),
      sleep: async () => {},
    }), (error) => {
      assert.match(error.message, new RegExp(`"message":"${message}"`));
      return true;
    });
  }
});

test('does not claim the daemon stopped when Docker state is unavailable', async () => {
  const calls = [];
  let logReads = 0;
  await assert.rejects(runNativeAmd64RuntimePreflight({
    ...inputs,
    runDocker: (args) => {
      calls.push(args);
      switch (commandKey(args)) {
        case 'info': return 'linux/amd64';
        case 'create': return 'preflight-container-id';
        case 'start': return inputs.containerName;
        case 'exec': throw new Error('private probe failure');
        case 'container inspect': return 'false 999';
        case 'container rm': return inputs.containerName;
        default: throw new Error(`unexpected docker command: ${args.join(' ')}`);
      }
    },
    readContainerLogs: () => {
      logReads += 1;
      return '';
    },
    sleep: async () => {},
  }), /native amd64 runtime preflight container state is unavailable/);
  assert.equal(logReads, 0, 'an unknown state must never trigger log collection');
  assert.equal(calls.filter((args) => commandKey(args) === 'container rm').length, 1);
});

test('reads logs only for canonical stopped exit codes from 0 through 255', async () => {
  for (const exitCode of [0, 255]) {
    let logReads = 0;
    await assert.rejects(runNativeAmd64RuntimePreflight({
      ...inputs,
      runDocker: (args) => {
        switch (commandKey(args)) {
          case 'info': return 'linux/amd64';
          case 'create': return 'preflight-container-id';
          case 'start': return inputs.containerName;
          case 'exec': throw new Error('probe failed');
          case 'container inspect': return `false ${exitCode}`;
          case 'container rm': return inputs.containerName;
          default: throw new Error(`unexpected docker command: ${args.join(' ')}`);
        }
      },
      readContainerLogs: () => { logReads += 1; return ''; },
      sleep: async () => {},
    }), new RegExp(`"exit_code":${exitCode}`));
    assert.equal(logReads, 1);
  }

  for (const state of ['false 256', 'false 025', 'false -1', 'malformed']) {
    let logReads = 0;
    await assert.rejects(runNativeAmd64RuntimePreflight({
      ...inputs,
      runDocker: (args) => {
        switch (commandKey(args)) {
          case 'info': return 'linux/amd64';
          case 'create': return 'preflight-container-id';
          case 'start': return inputs.containerName;
          case 'exec': throw new Error('probe failed');
          case 'container inspect': return state;
          case 'container rm': return inputs.containerName;
          default: throw new Error(`unexpected docker command: ${args.join(' ')}`);
        }
      },
      readContainerLogs: () => { logReads += 1; return ''; },
      sleep: async () => {},
    }), /native amd64 runtime preflight container state is unavailable/);
    assert.equal(logReads, 0, `state ${state} must not trigger log collection`);
  }
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
