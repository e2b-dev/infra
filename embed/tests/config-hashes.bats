#!/usr/bin/env bats

# Compose does not include the content of an inline `configs:` entry in the
# hash that decides whether a service is recreated on `up`, so a changed
# Vector or ClickHouse config would leave the old one running (seen on the
# GCE test VM: identical service hash before and after a content change).
# Each consuming service therefore carries the SHA-256 of its config file in
# its environment; the value is inert for the process but part of the service
# hash, so a content change recreates the container. These tests keep the
# stamped hashes equal to the files, alongside the byte-for-byte inline test.
# Needs Docker with the compose plugin and jq, like inline-configs.bats.

setup() {
  cd "$BATS_TEST_DIRNAME/.." || return 1
  RENDERED_JSON="$(docker compose --project-directory compose config --format json)"
}

@test "vector carries the sha256 of compose/config/vector/vector.toml" {
  want="$(sha256sum compose/config/vector/vector.toml | cut -d' ' -f1)"
  got="$(echo "$RENDERED_JSON" | jq -r '.services.vector.environment.VECTOR_CONFIG_SHA256')"
  [ "$got" = "$want" ]
}

@test "clickhouse carries the sha256 of compose/config/clickhouse/config.xml" {
  want="$(sha256sum compose/config/clickhouse/config.xml | cut -d' ' -f1)"
  got="$(echo "$RENDERED_JSON" | jq -r '.services.clickhouse.environment.CLICKHOUSE_CONFIG_SHA256')"
  [ "$got" = "$want" ]
}
