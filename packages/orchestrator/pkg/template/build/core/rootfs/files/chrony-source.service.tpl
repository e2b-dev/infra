{{- /*gotype:github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/core/rootfs.templateModel*/ -}}
{{ .WriteFile "etc/systemd/system/e2b-chrony-source.service" 0o644 }}

[Unit]
Description=E2B: select the chrony time source (PHC refclock if /dev/ptp0, else the NTP pool)
# The chrony unit is chrony.service on Debian and chronyd.service elsewhere;
# ordering before a unit that doesn't exist on this image is ignored. What pulls
# this in is a Requires= drop-in written for the family's unit name by
# provisioning (distro/init.go) — a drop-in, not an enablement symlink, because
# the RHEL family's preset policy ("disable *") deletes those on first boot.
Before=chrony.service chronyd.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/local/bin/e2b-chrony-source
