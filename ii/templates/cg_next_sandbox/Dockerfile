# with nodejs 20 python 3.12
# check: https://github.com/e2b-dev/code-interpreter/blob/99b7d8c7c79bead49741c33fa031538d4de35a4b/template/template.py#L4
FROM e2bdev/code-interpreter

USER root

RUN apt-get update && apt-get install -y --no-install-recommends \
  git \
  openssh-client \
  ca-certificates \
  curl \
  bash \
  tmux \
  vim \
  gnupg \
  gpg \
  lsb-release \
  fuse3 \
  && rm -rf /var/lib/apt/lists/*

RUN rm -f /etc/apt/sources.list.d/nodesource.list

# Install gcsfuse (official repo)
RUN set -eux; \
    . /etc/os-release; \
    GCSFUSE_REPO="gcsfuse-${VERSION_CODENAME}"; \
    mkdir -p /usr/share/keyrings; \
    # 建议加上 -L 跟踪重定向
    curl -fsSL https://packages.cloud.google.com/apt/doc/apt-key.gpg \
    | gpg --dearmor -o /usr/share/keyrings/cloud.google.gpg; \
    echo "deb [signed-by=/usr/share/keyrings/cloud.google.gpg] https://packages.cloud.google.com/apt ${GCSFUSE_REPO} main" \
    > /etc/apt/sources.list.d/gcsfuse.list; \
    # 这里执行时就不会再因为 NodeSource 的 SHA1 密钥报错了
    apt-get update; \
    apt-get install -y --no-install-recommends gcsfuse; \
    gcsfuse --version; \
    rm -rf /var/lib/apt/lists/*

# Install opencode
RUN curl -fsSL https://opencode.ai/install | bash

# Install pnpm
RUN wget -qO- https://get.pnpm.io/install.sh | ENV="$HOME/.bashrc" SHELL="$(which bash)" bash -
