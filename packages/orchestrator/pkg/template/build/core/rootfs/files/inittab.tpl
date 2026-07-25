{{- /*gotype:github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/core/rootfs.templateModel*/ -}}
{{ .WriteFile "/etc/inittab" 0o777 }}

# Provisioning-boot inittab (busybox init). Every entry is a plain exec with
# NO shell metacharacters: busybox init hands metachar lines to /bin/sh, and
# bare images (premade NixOS, distroless) have no /bin/sh — the pipeline
# logic lives in e2b-provision-runner instead (FEAT-145).

# Run system init (mounts /proc /sys /dev /tmp /run through the baked busybox)
::sysinit:/etc/init.d/rcS

# Run the provisioning pipeline and report its exit code
::wait:/usr/bin/busybox ash /usr/local/bin/e2b-provision-runner

# Wait forever to prevent the VM from exiting until the sandbox is paused and snapshot is taken
::wait:/usr/bin/busybox sleep infinity
