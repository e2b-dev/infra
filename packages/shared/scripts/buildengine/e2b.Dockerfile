FROM ubuntu:24.04

ARG NERDCTL_VERSION=2.1.2
ARG ARCH_TYPE=amd64

ENV LOCAL_DIR=/root/.local

RUN apt-get update && apt-get install -y \
    wget \
    tar \
    containerd \
    iptables \
    curl \
    && rm -rf /var/lib/apt/lists/*

RUN mkdir -p ${LOCAL_DIR}/bin ${LOCAL_DIR}/libexec

RUN wget -q "https://github.com/containerd/nerdctl/releases/download/v${NERDCTL_VERSION}/nerdctl-full-${NERDCTL_VERSION}-linux-${ARCH_TYPE}.tar.gz" -O /tmp/nerdctl.tar.gz \
    && tar -C ${LOCAL_DIR}/bin -xzf /tmp/nerdctl.tar.gz --strip-components=1 bin/nerdctl \
    && tar -C ${LOCAL_DIR} -xzf /tmp/nerdctl.tar.gz libexec \
    && rm /tmp/nerdctl.tar.gz

ENV PATH="${LOCAL_DIR}/bin:$PATH"

RUN nerdctl --version

RUN echo 'export PATH="${PATH}:~/.local/bin"' >> ~/.bashrc
RUN echo 'export CNI_PATH=~/.local/libexec/cni' >> ~/.bashrc

WORKDIR /dagger

RUN printf 'debug = true\ninsecure-entitlements = ["security.insecure"]\n\n[grpc]\naddress = ["tcp://0.0.0.0:1234"]\n' > engine.toml
RUN mkdir -p data