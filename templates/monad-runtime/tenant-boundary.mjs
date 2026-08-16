const SHA256 = /^[a-f0-9]{64}$/;

export const TENANT_BOUNDARY_IDENTITY = Object.freeze({
  uid: 911,
  gid: 1001,
  groups: Object.freeze([100]),
});

export function buildTenantBoundaryAttestation({
  daemonSha256,
  admissionHelperSha256,
}) {
  if (!SHA256.test(daemonSha256) || !SHA256.test(admissionHelperSha256)) {
    throw new Error('prepared tenant-boundary artifact SHA-256 is invalid');
  }
  return {
    schema_version: 1,
    kind: 'monad.session-rebind-tenant-boundary',
    daemon: {
      executable_path: '/opt/monad/runtime/bin/monad-agent',
      sha256: daemonSha256,
      uid: 0,
    },
    tenant: {
      uid: TENANT_BOUNDARY_IDENTITY.uid,
      gid: TENANT_BOUNDARY_IDENTITY.gid,
      groups: [...TENANT_BOUNDARY_IDENTITY.groups],
      services: {
        chromium: TENANT_BOUNDARY_IDENTITY.uid,
        git: TENANT_BOUNDARY_IDENTITY.uid,
        opencode: TENANT_BOUNDARY_IDENTITY.uid,
        selkies: TENANT_BOUNDARY_IDENTITY.uid,
        xorg: TENANT_BOUNDARY_IDENTITY.uid,
      },
    },
    cgroup: {
      version: 2,
      single_tenant_subtree: true,
      kill: true,
      detached_descendant_probe: true,
      root_daemon_survives_kill: true,
    },
    admission_helper: {
      path: '/opt/monad/runtime/libexec/monad-tenant-admission',
      sha256: admissionHelperSha256,
      protocol_version: 3,
    },
    runtime_config_digest: {
      algorithm: 'monad.runtime-config.sha256.v1',
    },
  };
}
