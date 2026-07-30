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
COPY .build-assets/monad /usr/local/bin/monad
COPY .build-assets/entrypoint.sh /usr/local/bin/monad-entrypoint
COPY .build-assets/agent-cli/ /opt/monad/apps/sandbox/agent-cli/
COPY .build-assets/executor-sdk/ /opt/monad/packages/executor-sdk/
COPY s6-overlay/s6-rc.d/svc-monad-agent/ /etc/s6-overlay/s6-rc.d/svc-monad-agent/

RUN chmod +x \
        /usr/local/bin/monad-agent \
        /usr/local/bin/monad \
        /usr/local/bin/monad-entrypoint \
        /etc/s6-overlay/s6-rc.d/svc-monad-agent/run \
        /opt/monad/apps/sandbox/agent-cli/install-shims.sh \
    && touch /etc/s6-overlay/s6-rc.d/user/contents.d/svc-monad-agent \
    && bash /opt/monad/apps/sandbox/agent-cli/install-shims.sh /opt/monad/apps/sandbox/agent-cli \
    && bun --version \
    && monad --version

ENV MONAD_WORKSPACE=/workspace
ENV PLAYWRIGHT_BROWSERS_PATH=0
ENV AGENT_BROWSER_EXECUTABLE_PATH=/usr/bin/chromium
ENV AGENT_BROWSER_ARGS=--no-sandbox,--disable-dev-shm-usage
ENV CUSTOM_PORT=6080
ENV CUSTOM_HTTPS_PORT=6081
ENV START_DOCKER=false

USER root
WORKDIR /workspace
EXPOSE 8000 6080 6081
