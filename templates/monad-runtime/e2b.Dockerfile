FROM oven/bun:1-debian

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        git \
        nodejs \
        npm \
        unzip \
    && rm -rf /var/lib/apt/lists/*

RUN npm install -g --no-audit --no-fund \
        opencode-ai@1.14.28 \
        agent-browser@0.27.0 \
        playwright@1.60.0 \
    && apt-get update \
    && PLAYWRIGHT_BROWSERS_PATH=/opt/pw-browsers playwright install --with-deps chromium \
    && rm -rf /var/lib/apt/lists/* \
    && ln -sf "$(find /opt/pw-browsers -type f -path '*chrome-linux*/chrome' | head -n1)" /usr/local/bin/chromium \
    && opencode --version \
    && agent-browser --version \
    && playwright --version \
    && chromium --version

RUN mkdir -p /opt/monad/home/.bun/install/cache /tmp/oc-deps \
    && cd /tmp/oc-deps \
    && printf '{"dependencies":{"@mendable/firecrawl-js":"^4.25.1","@tavily/core":"^0.7.3","replicate":"^1.4.0"}}' > package.json \
    && BUN_INSTALL_CACHE_DIR=/opt/monad/home/.bun/install/cache bun install \
    && rm -rf /tmp/oc-deps

COPY .build-assets/monad-agent /usr/local/bin/monad-agent
COPY .build-assets/monad /usr/local/bin/monad
COPY .build-assets/entrypoint.sh /usr/local/bin/monad-entrypoint
COPY .build-assets/agent-cli/ /opt/monad/apps/sandbox/agent-cli/
COPY .build-assets/executor-sdk/ /opt/monad/packages/executor-sdk/

RUN chmod +x \
        /usr/local/bin/monad-agent \
        /usr/local/bin/monad \
        /usr/local/bin/monad-entrypoint \
        /opt/monad/apps/sandbox/agent-cli/install-shims.sh \
    && bash /opt/monad/apps/sandbox/agent-cli/install-shims.sh /opt/monad/apps/sandbox/agent-cli \
    && monad --version

ENV MONAD_WORKSPACE=/workspace
ENV PLAYWRIGHT_BROWSERS_PATH=/opt/pw-browsers
ENV AGENT_BROWSER_EXECUTABLE_PATH=/usr/local/bin/chromium
ENV AGENT_BROWSER_ARGS=--no-sandbox,--disable-dev-shm-usage

USER root
WORKDIR /workspace
EXPOSE 8000
