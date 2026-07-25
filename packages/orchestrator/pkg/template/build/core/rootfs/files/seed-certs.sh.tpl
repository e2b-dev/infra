{{- /*gotype:github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/core/rootfs.templateModel*/ -}}
{{ .WriteFile "usr/local/bin/e2b-seed-certs" 0o755 }}

#!/bin/sh
# Seeds the tmpfs-backed /etc/ssl/certs before envd starts — shared by
# envd.service (systemd) and /etc/init.d/envd (OpenRC). See envd.service.tpl
# for the full rationale (why a tar, why a bind mount, the egress-CA contract).
#
# Every failure path here WARNS and continues deliberately: a sandbox with a
# degraded trust store is recoverable (envd's POST /init reinstalls the egress
# CA), a sandbox whose envd never starts is not.

if ! mountpoint -q /etc/ssl/certs; then
    mkdir -p /run/e2b/certs
    if [ -f /usr/local/share/e2b/ssl-certs.tar ]; then
        if ! tar -C /run/e2b/certs -xf /usr/local/share/e2b/ssl-certs.tar; then
            echo "e2b-seed-certs: ssl-certs.tar extraction failed; seeding from the live cert dir instead" >&2
            cp -a /etc/ssl/certs/. /run/e2b/certs/
        fi
    else
        # Only expected during the base-layer boot, before finalize packs the tar.
        echo "e2b-seed-certs: ssl-certs.tar not packed yet; seeding from the live cert dir"
        cp -a /etc/ssl/certs/. /run/e2b/certs/
    fi
    if ! mount -o bind /run/e2b/certs /etc/ssl/certs; then
        echo "e2b-seed-certs: bind mount failed; envd runs with the image's certs as-is" >&2
    fi
fi

if [ ! -s /etc/ssl/certs/ca-certificates.crt ]; then
    if command -v update-ca-certificates >/dev/null 2>&1; then
        update-ca-certificates
    else
        # Provisioning guarantees the bundle on every supported family;
        # reaching this means the image diverged after the build.
        echo "e2b-seed-certs: CA bundle missing and no update-ca-certificates on this image; TLS trust will be degraded" >&2
    fi
fi

exit 0
