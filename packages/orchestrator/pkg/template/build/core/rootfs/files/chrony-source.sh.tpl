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

mkdir -p /run/chrony-e2b
if [ -e /dev/ptp0 ]; then
    echo "refclock PHC /dev/ptp0 poll 2 dpoll 2" >/run/chrony-e2b/source.conf
else
    echo "pool pool.ntp.org iburst maxsources 3" >/run/chrony-e2b/source.conf
fi
