{{- /*gotype:github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/core/rootfs.templateModel*/ -}}
{{ .WriteFile "usr/local/bin/e2b-chrony-source" 0o755 }}

#!/bin/sh
# Writes the chrony time source the machine we are booting on can actually use.
# The baked /etc/chrony/chrony.conf includes the file this produces.
#
# The hypervisor's PTP clock (kvm-ptp) is the better source — no network, tracks
# the host directly — but it is absent wherever nested virtualization can't
# expose it, and a refclock line for a missing PHC is FATAL to chronyd. Template
# builds and sandboxes run in separate node pools (docs/ARCHITECTURE.md), so the
# device present while provisioning says nothing about the node a cold-booting
# sandbox lands on; only a boot-time decision is right on both.
#
# Shared by e2b-chrony-source.service (systemd) and the OpenRC service of the
# same name (Alpine).
set -eu

# Record which source was chosen so an operator can tell, after the fact, why a
# guest's clock did or didn't converge. The fallback to the public NTP pool is
# the slow, network-dependent path; when it is taken silently there is nothing
# to inspect after boot. We log to stderr (captured by the systemd journal / the
# OpenRC service log), to syslog via logger when present, and to a stable marker
# file that survives for the life of the boot.
log() {
    echo "e2b-chrony-source: $1" >&2
    if command -v logger >/dev/null 2>&1; then
        logger -t e2b-chrony-source "$1" || true
    fi
}

mkdir -p /run/chrony-e2b
if [ -e /dev/ptp0 ]; then
    source_line="refclock PHC /dev/ptp0 poll 2 dpoll 2"
    selected="phc"
    log "/dev/ptp0 present: using hypervisor PHC refclock"
else
    source_line="pool pool.ntp.org iburst maxsources 3"
    selected="pool"
    log "/dev/ptp0 absent: falling back to public NTP pool (pool.ntp.org) — first sync races iburst convergence over the network"
fi

echo "$source_line" >/run/chrony-e2b/source.conf
echo "$selected" >/run/chrony-e2b/selected
