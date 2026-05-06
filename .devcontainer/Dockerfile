FROM mcr.microsoft.com/devcontainers/go:2.0.3-1.25-bookworm

# The base image includes a Yarn apt source with an expired/missing key, which
# breaks feature installation when apt-get update runs.
RUN rm -f /etc/apt/sources.list.d/yarn.list
