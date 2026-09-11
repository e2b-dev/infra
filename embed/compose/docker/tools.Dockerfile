FROM alpine:3.20
RUN apk upgrade --no-cache && apk add --no-cache bash util-linux curl jq coreutils ca-certificates
# The one-shot host scripts and the orchestrator launcher travel inside the
# image, so compose.yaml needs no repository mount.
COPY scripts/preflight.sh scripts/host-setup.sh scripts/fetch-artifacts.sh \
     scripts/host-teardown.sh scripts/orchestrator-launch.sh /opt/e2b/scripts/
