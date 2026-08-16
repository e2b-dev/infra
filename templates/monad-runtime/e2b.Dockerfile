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
        opencode-ai@1.14.28 \
        agent-browser@0.27.0 \
        playwright@1.60.0 \
    && opencode --version \
    && agent-browser --version \
    && playwright --version \
    && /usr/bin/chromium --version \
    && rm -rf /tmp/npm-cache

COPY .build-assets/bun /usr/local/bin/bun

RUN chmod +x /usr/local/bin/bun \
    && ln -sf /usr/local/bin/bun /usr/local/bin/bunx

RUN mkdir -p /opt/monad/home/.bun/install/cache /tmp/oc-deps \
    && cd /tmp/oc-deps \
    && printf '{"dependencies":{"@mendable/firecrawl-js":"4.31.1","@tavily/core":"0.7.6","replicate":"1.4.0"}}' > package.json \
    && BUN_INSTALL_CACHE_DIR=/opt/monad/home/.bun/install/cache bun install \
    && rm -rf /tmp/oc-deps

COPY .build-assets/monad-agent /usr/local/bin/monad-agent
COPY .build-assets/monad-tenant-admission /usr/local/libexec/monad-tenant-admission
COPY .build-assets/session-rebind-tenant-boundary.json /etc/monad/session-rebind-tenant-boundary.json

# Fail the build before init can ever see bytes that differ from the prepared
# same-descriptor hashes bound into the immutable tenant-boundary claim.
RUN node -e 'const {execFileSync}=require("node:child_process"); const attestation=require("/etc/monad/session-rebind-tenant-boundary.json"); const sha256=(path)=>execFileSync("sha256sum",[path],{encoding:"utf8"}).trim().split(/\s+/)[0]; if(attestation.daemon.sha256!==sha256("/usr/local/bin/monad-agent")) throw new Error("prepared daemon hash mismatch"); if(attestation.admission_helper.sha256!==sha256("/usr/local/libexec/monad-tenant-admission")) throw new Error("prepared admission helper hash mismatch");'

COPY .build-assets/monad /usr/local/bin/monad
COPY .build-assets/entrypoint.sh /usr/local/bin/monad-entrypoint
COPY .build-assets/agent-cli/ /opt/monad/apps/sandbox/agent-cli/
COPY .build-assets/executor-sdk/ /opt/monad/packages/executor-sdk/

# Preserve the exact pinned Webtop service implementations behind the same
# statically linked admission helper whose hash the daemon attests. The copied
# scripts use a plain Bash shebang because the s6 wrapper already imports the
# container environment; the join-only helper removes loader and shell startup
# hooks before executing the upstream root setup inside the tenant cgroup. The
# upstream scripts retain their internal s6-setuidgid transitions.
RUN install -d -o root -g root -m 0755 /usr/local/libexec /etc/monad \
    && install -o root -g root -m 0555 \
        /etc/s6-overlay/s6-rc.d/svc-nginx/run \
        /usr/local/libexec/monad-webtop-svc-nginx \
    && install -o root -g root -m 0555 \
        /etc/s6-overlay/s6-rc.d/svc-xorg/run \
        /usr/local/libexec/monad-webtop-svc-xorg \
    && install -o root -g root -m 0555 \
        /etc/s6-overlay/s6-rc.d/svc-dbus/run \
        /usr/local/libexec/monad-webtop-svc-dbus \
    && install -o root -g root -m 0555 \
        /etc/s6-overlay/s6-rc.d/svc-pulseaudio/run \
        /usr/local/libexec/monad-webtop-svc-pulseaudio \
    && install -o root -g root -m 0555 \
        /etc/s6-overlay/s6-rc.d/svc-selkies/run \
        /usr/local/libexec/monad-webtop-svc-selkies \
    && install -o root -g root -m 0555 \
        /etc/s6-overlay/s6-rc.d/svc-de/run \
        /usr/local/libexec/monad-webtop-svc-de \
    && install -o root -g root -m 0555 \
        /etc/s6-overlay/s6-rc.d/svc-watchdog/run \
        /usr/local/libexec/monad-webtop-svc-watchdog \
    && install -o root -g root -m 0555 \
        /etc/s6-overlay/s6-rc.d/svc-xsettingsd/run \
        /usr/local/libexec/monad-webtop-svc-xsettingsd \
    && sed -i '1c#!/bin/bash' \
        /usr/local/libexec/monad-webtop-svc-nginx \
        /usr/local/libexec/monad-webtop-svc-xorg \
        /usr/local/libexec/monad-webtop-svc-dbus \
        /usr/local/libexec/monad-webtop-svc-pulseaudio \
        /usr/local/libexec/monad-webtop-svc-selkies \
        /usr/local/libexec/monad-webtop-svc-de \
        /usr/local/libexec/monad-webtop-svc-watchdog \
        /usr/local/libexec/monad-webtop-svc-xsettingsd \
    && bash -n \
        /usr/local/libexec/monad-webtop-svc-nginx \
        /usr/local/libexec/monad-webtop-svc-xorg \
        /usr/local/libexec/monad-webtop-svc-dbus \
        /usr/local/libexec/monad-webtop-svc-pulseaudio \
        /usr/local/libexec/monad-webtop-svc-selkies \
        /usr/local/libexec/monad-webtop-svc-de \
        /usr/local/libexec/monad-webtop-svc-watchdog \
        /usr/local/libexec/monad-webtop-svc-xsettingsd

COPY s6-overlay/s6-rc.d/ /etc/s6-overlay/s6-rc.d/

RUN chmod +x \
        /usr/local/bin/monad-agent \
        /usr/local/libexec/monad-tenant-admission \
        /usr/local/bin/monad \
        /usr/local/bin/monad-entrypoint \
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
    && test "$(id -G abc)" = "1001 100" \
    && test "$(getent group 100)" = "users:x:100:abc" \
    && ! getent group sudo | grep -Eq '(^|[:,])abc(,|$)' \
    && ! getent group docker | grep -Eq '(^|[:,])abc(,|$)' \
    && grep -Fxq 'exec sleep infinity' /etc/s6-overlay/s6-rc.d/svc-cron/run \
    && mkdir -p /workspace \
    && chown -R 911:1001 /opt/monad/home /workspace \
    && touch /etc/s6-overlay/s6-rc.d/user/contents.d/svc-monad-agent \
    && bash /opt/monad/apps/sandbox/agent-cli/install-shims.sh /opt/monad/apps/sandbox/agent-cli \
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
