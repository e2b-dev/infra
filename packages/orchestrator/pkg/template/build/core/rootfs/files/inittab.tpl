{{- /*gotype:github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/core/rootfs.templateModel*/ -}}
{{ .WriteFile "/etc/inittab" 0o777 }}

# Run system init
::sysinit:/etc/init.d/rcS

# Run the provision script, prefix the output with a log prefix.
# Everything goes through the baked busybox: bare images (pure-Nix, distroless)
# ship no /bin/sh or sed, and the rejection message must still reach the build
# log (FEAT-145 AC4) — provisioning is exactly where such images fail.
::wait:/usr/bin/busybox sh -c '/usr/bin/busybox sh /usr/local/bin/provision.sh 2>&1 | /usr/bin/busybox sed "s/^/{{ .ProvisionLogPrefix }}/"'

# Flush filesystem changes to disk
::wait:/usr/bin/busybox sync
::wait:fsfreeze --freeze /

# Report the exit code of the provisioning script
::wait:/usr/bin/busybox sh -c 'echo "{{ .ProvisionExitPrefix }}$(cat {{ .ProvisionResultPath }} || printf 1)"'

# Wait forever to prevent the VM from exiting until the sandbox is paused and snapshot is taken
::wait:/usr/bin/busybox sleep infinity