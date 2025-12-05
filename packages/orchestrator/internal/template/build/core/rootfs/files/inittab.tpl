{{- /*gotype:github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/core/rootfs.templateModel*/ -}}
{{ .WriteFile "/etc/inittab" 0o777 }}

# Run system init
::sysinit:/etc/init.d/rcS

# Run the provision script, prefix the output with a log prefix
::wait:/bin/sh -c '/usr/local/bin/provision.sh 2>&1 | sed "s/^/{{ .ProvisionLogPrefix }}/"'

# Flush filesystem changes to disk
::wait:/usr/bin/busybox sync
::wait:fsfreeze --freeze /

# Report the exit code of the provisioning script
::wait:/bin/sh -c 'echo "{{ .ProvisionExitPrefix }}$(cat {{ .ProvisionResultPath }} || printf 1)"'

# Wait forever to prevent the VM from exiting until the sandbox is paused and snapshot is taken
::wait:/usr/bin/busybox sleep infinity