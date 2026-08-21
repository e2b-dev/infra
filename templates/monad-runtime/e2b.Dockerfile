FROM lscr.io/linuxserver/webtop@sha256:6da6d68a2e3309fdbdc945a1b5465c3174acdf19dfbd44734495b122f10a5236

ENV DEBIAN_FRONTEND=noninteractive
ENV NPM_CONFIG_CACHE=/tmp/npm-cache

RUN apt-get update \
    && apt-get purge -y \
        containerd.io \
        docker-buildx-plugin \
        docker-ce \
        docker-ce-cli \
        docker-compose-plugin \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        git \
        nodejs \
        unzip \
    && rm -rf /config/.npm /tmp/npm-cache /var/lib/apt/lists/* /var/lib/docker

RUN npm install -g --no-audit --no-fund \
        opencode-ai@1.18.19 \
        agent-browser@0.27.0 \
        playwright@1.60.0 \
    && opencode --version \
    && agent-browser --version \
    && playwright --version \
    && /usr/bin/chromium --version \
    && rm -rf /tmp/npm-cache

# The tams monorepo pins `"packageManager": "pnpm@8.11.0"`, and its monad.toml
# workspace setup runs `pnpm install --frozen-lockfile` (on_boot: `pnpm dev`).
# Without pnpm on this image that step exit-127's for every session on the
# fleet: it was the root cause behind three separate manifest workarounds
# (tams #540, #542, #545). Install via `npm install -g` — REAL FILES under the
# npm prefix — and NOT `corepack prepare --activate`: corepack activation is
# cache/shim state that passed every build-time self-check here but did NOT
# survive into the running E2B sandbox; at runtime the shim fell back to
# corepack's own NETWORK resolution (registry.npmjs.org) and failed closed in
# the egress-restricted workcell (rotation run 32470431726, 2026-08-21 — the
# CommandExitError-diagnostics fix in verify-runtime finally surfaced it).
# The self-check below runs with corepack's network hard-disabled and its
# home pointed at a nonexistent dir, so any regression back to
# activation-state-only pnpm fails THIS BUILD, not the fleet.
RUN npm install -g pnpm@8.11.0 \
    && test "$(pnpm --version)" = 8.11.0 \
    && pnpm_real="$(readlink -f "$(command -v pnpm)")" \
    && pnpx_real="$(readlink -f "$(command -v pnpx)")" \
    && ln -sf "$pnpm_real" /usr/local/bin/pnpm \
    && ln -sf "$pnpx_real" /usr/local/bin/pnpx \
    && sh -lc 'command -v pnpm' \
    && sh -c 'command -v pnpm' \
    && COREPACK_ENABLE_NETWORK=0 COREPACK_HOME=/nonexistent sh -lc 'test "$(pnpm --version)" = 8.11.0' \
    && COREPACK_ENABLE_NETWORK=0 COREPACK_HOME=/nonexistent sh -c 'test "$(pnpm --version)" = 8.11.0' \
    && ! head -c 512 "$(readlink -f /usr/local/bin/pnpm)" | grep -qi corepack

COPY .build-assets/bun /usr/local/bin/bun

RUN chmod +x /usr/local/bin/bun \
    && ln -sf /usr/local/bin/bun /usr/local/bin/bunx

RUN mkdir -p /opt/monad/home/.bun/install/cache /tmp/oc-deps \
    && cd /tmp/oc-deps \
    && printf '{"dependencies":{"@mendable/firecrawl-js":"4.31.1","@tavily/core":"0.7.6","replicate":"1.4.0"}}' > package.json \
    && BUN_INSTALL_CACHE_DIR=/opt/monad/home/.bun/install/cache bun install \
    && rm -rf /tmp/oc-deps

RUN install -d -o root -g root -m 0755 \
        /opt \
        /opt/monad \
        /opt/monad/runtime \
        /opt/monad/runtime/bin \
        /opt/monad/runtime/libexec

COPY .build-assets/monad-agent /opt/monad/runtime/bin/monad-agent
COPY .build-assets/monad-tenant-admission /opt/monad/runtime/libexec/monad-tenant-admission
COPY .build-assets/session-rebind-tenant-boundary.json /etc/monad/session-rebind-tenant-boundary.json

# Fail the build before init can ever see bytes that differ from the prepared
# same-descriptor hashes bound into the immutable tenant-boundary claim.
RUN node -e 'const {execFileSync}=require("node:child_process"); const attestation=require("/etc/monad/session-rebind-tenant-boundary.json"); const sha256=(path)=>{const digest=execFileSync("sha256sum",[path],{encoding:"utf8"}).slice(0,64); if(!/^[a-f0-9]{64}$/.test(digest)) throw new Error("invalid sha256sum output"); return digest;}; if(attestation.daemon.sha256!==sha256("/opt/monad/runtime/bin/monad-agent")) throw new Error("prepared daemon hash mismatch"); if(attestation.admission_helper.sha256!==sha256("/opt/monad/runtime/libexec/monad-tenant-admission")) throw new Error("prepared admission helper hash mismatch");'

COPY .build-assets/monad /usr/local/bin/monad
COPY .build-assets/entrypoint.sh /opt/monad/runtime/bin/monad-entrypoint
COPY .build-assets/agent-cli/ /opt/monad/apps/sandbox/agent-cli/
COPY .build-assets/executor-sdk/ /opt/monad/packages/executor-sdk/

# Preserve the exact pinned Webtop service implementations behind the same
# statically linked admission helper whose hash the daemon attests. The copied
# scripts use a plain Bash shebang because the s6 wrapper already imports the
# container environment; the join-only helper removes loader and shell startup
# hooks before executing the upstream root setup inside the tenant cgroup. The
# upstream scripts retain their internal s6-setuidgid transitions.
RUN install -d -o root -g root -m 0755 /opt/monad/runtime/libexec /etc/monad \
    && install -o root -g root -m 0555 \
        /etc/s6-overlay/s6-rc.d/svc-nginx/run \
        /opt/monad/runtime/libexec/monad-webtop-svc-nginx \
    && install -o root -g root -m 0555 \
        /etc/s6-overlay/s6-rc.d/svc-xorg/run \
        /opt/monad/runtime/libexec/monad-webtop-svc-xorg \
    && install -o root -g root -m 0555 \
        /etc/s6-overlay/s6-rc.d/svc-dbus/run \
        /opt/monad/runtime/libexec/monad-webtop-svc-dbus \
    && install -o root -g root -m 0555 \
        /etc/s6-overlay/s6-rc.d/svc-pulseaudio/run \
        /opt/monad/runtime/libexec/monad-webtop-svc-pulseaudio \
    && install -o root -g root -m 0555 \
        /etc/s6-overlay/s6-rc.d/svc-selkies/run \
        /opt/monad/runtime/libexec/monad-webtop-svc-selkies \
    && install -o root -g root -m 0555 \
        /etc/s6-overlay/s6-rc.d/svc-de/run \
        /opt/monad/runtime/libexec/monad-webtop-svc-de \
    && install -o root -g root -m 0555 \
        /etc/s6-overlay/s6-rc.d/svc-watchdog/run \
        /opt/monad/runtime/libexec/monad-webtop-svc-watchdog \
    && install -o root -g root -m 0555 \
        /etc/s6-overlay/s6-rc.d/svc-xsettingsd/run \
        /opt/monad/runtime/libexec/monad-webtop-svc-xsettingsd \
    && sed -i '1c#!/bin/bash' \
        /opt/monad/runtime/libexec/monad-webtop-svc-nginx \
        /opt/monad/runtime/libexec/monad-webtop-svc-xorg \
        /opt/monad/runtime/libexec/monad-webtop-svc-dbus \
        /opt/monad/runtime/libexec/monad-webtop-svc-pulseaudio \
        /opt/monad/runtime/libexec/monad-webtop-svc-selkies \
        /opt/monad/runtime/libexec/monad-webtop-svc-de \
        /opt/monad/runtime/libexec/monad-webtop-svc-watchdog \
        /opt/monad/runtime/libexec/monad-webtop-svc-xsettingsd \
    && bash -n \
        /opt/monad/runtime/libexec/monad-webtop-svc-nginx \
        /opt/monad/runtime/libexec/monad-webtop-svc-xorg \
        /opt/monad/runtime/libexec/monad-webtop-svc-dbus \
        /opt/monad/runtime/libexec/monad-webtop-svc-pulseaudio \
        /opt/monad/runtime/libexec/monad-webtop-svc-selkies \
        /opt/monad/runtime/libexec/monad-webtop-svc-de \
        /opt/monad/runtime/libexec/monad-webtop-svc-watchdog \
        /opt/monad/runtime/libexec/monad-webtop-svc-xsettingsd

# The E2B Dockerfile renderer consumes command-string backslashes. Transport
# this digest-bound helper as a file so the exact pinned continuation line and
# line-feed parsing cannot be altered before the image build executes them.
COPY selkies-launcher-rewrite.mjs /tmp/monad-selkies-launcher-rewrite.mjs

RUN test -x /lsiopy/bin/selkies \
    && node /tmp/monad-selkies-launcher-rewrite.mjs \
        /opt/monad/runtime/libexec/monad-webtop-svc-selkies \
    && rm -f /tmp/monad-selkies-launcher-rewrite.mjs

COPY s6-overlay/s6-rc.d/ /etc/s6-overlay/s6-rc.d/

RUN chmod +x \
        /opt/monad/runtime/bin/monad-agent \
        /opt/monad/runtime/libexec/monad-tenant-admission \
        /usr/local/bin/monad \
        /opt/monad/runtime/bin/monad-entrypoint \
        /etc/s6-overlay/s6-rc.d/svc-monad-agent/run \
        /etc/s6-overlay/s6-rc.d/svc-nginx/run \
        /etc/s6-overlay/s6-rc.d/svc-xorg/run \
        /etc/s6-overlay/s6-rc.d/svc-dbus/run \
        /etc/s6-overlay/s6-rc.d/svc-pulseaudio/run \
        /etc/s6-overlay/s6-rc.d/svc-selkies/run \
        /etc/s6-overlay/s6-rc.d/svc-de/run \
        /etc/s6-overlay/s6-rc.d/svc-watchdog/run \
        /etc/s6-overlay/s6-rc.d/svc-xsettingsd/run \
        /etc/s6-overlay/s6-rc.d/svc-cron/run \
        /opt/monad/apps/sandbox/agent-cli/install-shims.sh \
    && chmod 0444 /etc/monad/session-rebind-tenant-boundary.json \
    && test "$(id -u abc):$(id -g abc)" = "911:1001" \
    && gpasswd -d abc sudo \
    && gpasswd -d abc docker \
    && abc_groups="$(id -G abc | xargs -n1 | sort -n | paste -sd, -)" \
    && echo "Attested abc groups: $abc_groups" \
    && test "$abc_groups" = "100,1001" \
    && grep -Fxq 'exec sleep infinity' /etc/s6-overlay/s6-rc.d/svc-cron/run \
    && echo "Attested cron override: inactive" \
    && mkdir -p /workspace \
    && chown -R 911:1001 /opt/monad/home /workspace \
    && touch /etc/s6-overlay/s6-rc.d/user/contents.d/svc-monad-agent \
    && bash /opt/monad/apps/sandbox/agent-cli/install-shims.sh /opt/monad/apps/sandbox/agent-cli \
    && test -d /opt/monad/runtime/bin \
    && test ! -L /opt/monad/runtime/bin \
    && install -d -o root -g root -m 0755 /opt/monad/runtime/bin \
    && for runtime_directory in /opt /opt/monad /opt/monad/runtime /opt/monad/runtime/libexec; do \
        test -d "$runtime_directory"; \
        test ! -L "$runtime_directory"; \
    done \
    && install -d -o root -g root -m 0755 \
        /opt \
        /opt/monad \
        /opt/monad/runtime \
        /opt/monad/runtime/libexec \
    && test -d /opt/monad/runtime/bin \
    && test ! -L /opt/monad/runtime/bin \
    && test "$(stat -c '%u:%g:%a' -- /opt/monad/runtime/bin)" = "0:0:755" \
    && test -f /opt/monad/runtime/bin/monad-agent \
    && test ! -L /opt/monad/runtime/bin/monad-agent \
    && test "$(stat -c '%u:%g:%a' -- /opt/monad/runtime/bin/monad-agent)" = "0:0:755" \
    && test -f /opt/monad/runtime/bin/monad-entrypoint \
    && test ! -L /opt/monad/runtime/bin/monad-entrypoint \
    && test "$(stat -c '%u:%g:%a' -- /opt/monad/runtime/bin/monad-entrypoint)" = "0:0:755" \
    && for runtime_directory in /opt /opt/monad /opt/monad/runtime /opt/monad/runtime/libexec; do \
        test -d "$runtime_directory"; \
        test ! -L "$runtime_directory"; \
        test "$(stat -c '%u:%g:%a' -- "$runtime_directory")" = "0:0:755"; \
    done \
    && test -f /opt/monad/runtime/libexec/monad-tenant-admission \
    && test ! -L /opt/monad/runtime/libexec/monad-tenant-admission \
    && test "$(stat -c '%u:%g:%a' -- /opt/monad/runtime/libexec/monad-tenant-admission)" = "0:0:755" \
    && bun --version \
    && monad --version

ENV MONAD_WORKSPACE=/workspace
# E2B create-time env vars reach envd-exec'd commands only — never the
# s6-supervised daemon (proved live 2026-08-05: daemon environ carries image
# ENV via with-contenv/container_environment, but not sandbox create env).
# Every E2B workcell — ordinary session or parked warm pool — must boot
# credential-free and await the private stdin bootstrap, so the flag is a
# property of this template, baked here where with-contenv delivers it.
ENV MONAD_CREDENTIAL_BOOTSTRAP_REQUIRED=1
ENV MONAD_TENANT_BOUNDARY_REQUIRED=1
ENV PUID=911
ENV PGID=1001
ENV PLAYWRIGHT_BROWSERS_PATH=0
ENV AGENT_BROWSER_EXECUTABLE_PATH=/usr/bin/chromium
ENV AGENT_BROWSER_ARGS=--no-sandbox,--disable-dev-shm-usage
ENV CUSTOM_PORT=6080
ENV CUSTOM_HTTPS_PORT=6081
ENV START_DOCKER=false
ENV RESTART_APP=false

USER root
WORKDIR /workspace
EXPOSE 8000 6080 6081
