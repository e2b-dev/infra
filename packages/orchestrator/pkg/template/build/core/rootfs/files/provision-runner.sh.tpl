{{- /*gotype:github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/core/rootfs.templateModel*/ -}}
{{ .WriteFile "usr/local/bin/e2b-provision-runner" 0o755 }}

#!/usr/bin/busybox ash
# Drives the provisioning pipeline for the busybox-init boot. This logic lives
# in a script — NOT in /etc/inittab — because busybox init hands any inittab
# line containing shell metacharacters to /bin/sh, and minimal images
# (distroless) may have no /bin/sh; a plain-exec inittab line running this
# script through the baked busybox works on every image.
BB=/usr/bin/busybox

# Run the provision script, prefix its output with the log prefix the
# orchestrator forwards to the customer's build logs.
$BB sh /usr/local/bin/provision.sh 2>&1 | $BB sed "s/^/{{ .ProvisionLogPrefix }}/"

# Flush filesystem changes to disk before the snapshot.
$BB sync
if command -v fsfreeze >/dev/null 2>&1; then
    fsfreeze --freeze /
else
    # No util-linux on this image; the double sync flushes the ext4 journal
    # and the VM is paused before the snapshot is taken.
    echo "fsfreeze not available on this image; using sync-only flush"
    $BB sync
fi

# Report the provisioning exit code: provision.sh writes "0" on success and
# (running under set -e) leaves no file behind on failure.
if result=$($BB cat {{ .ProvisionResultPath }} 2>/dev/null); then
    echo "{{ .ProvisionExitPrefix }}${result}"
else
    echo "{{ .ProvisionExitPrefix }}1"
fi
