-- +goose Up
-- +goose StatementBegin
CREATE TABLE sandbox_egress_local (
    -- Written by the node, so both are subject to its clock. The partition and
    -- the TTL key on ingested_at instead, which the server assigns: a node with
    -- a skewed clock would otherwise write a partition that never expires.
    first_seen DateTime64(9) CODEC (Delta, ZSTD(1)),
    last_seen DateTime64(9) CODEC (Delta, ZSTD(1)),
    ingested_at DateTime64(9) DEFAULT now64(9) CODEC (Delta, ZSTD(1)),
    team_id UUID CODEC (ZSTD(1)),
    sandbox_id String CODEC (ZSTD(1)),
    sandbox_execution_id String CODEC (ZSTD(1)),
    sandbox_template_id String CODEC (ZSTD(1)),
    sandbox_build_id String CODEC (ZSTD(1)),
    sandbox_type LowCardinality(String) CODEC (ZSTD(1)),
    protocol LowCardinality(String) CODEC (ZSTD(1)),
    -- String rather than IPv6 on purpose: the IPv6 type stores a v4 address as
    -- ::ffff:a.b.c.d, for which isIPAddressInRange answers false against a v4
    -- CIDR. On a String it answers directly.
    destination_ip String CODEC (ZSTD(1)),
    destination_port UInt16 CODEC (ZSTD(1)),
    -- NULL when the connection exposed no server name: every non-TLS/HTTP port,
    -- a TLS handshake without SNI, and an HTTP request without a Host header.
    -- An empty string cannot stand in for that absence. An HTTP request to an
    -- IP literal does carry a Host header, and records the address here.
    server_name Nullable(String) CODEC (ZSTD(1)),
    decision LowCardinality(String) CODEC (ZSTD(1)),
    match_type LowCardinality(String) CODEC (ZSTD(1)),
    -- Connections the firewall reached this verdict for, between first_seen and
    -- last_seen. Rows are pre-aggregated per flush interval, so a full history
    -- needs sum(connections) over the range.
    --
    -- A row records a verdict, not a completed connection: it is written when
    -- the firewall admits or refuses the connection, before the upstream dial,
    -- which can still fail.
    connections UInt64 CODEC (ZSTD(1)),
    -- The sorting key leads with the team, so a lookup that starts from a
    -- destination rather than a team would otherwise scan.
    INDEX idx_destination_ip destination_ip TYPE bloom_filter GRANULARITY 4,
    INDEX idx_server_name server_name TYPE bloom_filter GRANULARITY 4,
    -- Investigating one sandbox without knowing its team cannot use the sorting
    -- key either.
    INDEX idx_sandbox_id sandbox_id TYPE bloom_filter GRANULARITY 4
) ENGINE = MergeTree
    PARTITION BY toDate(ingested_at)
    -- The destination precedes the timestamp because every intended read groups
    -- by destination over a range of intervals: the repeated rows one endpoint
    -- produces then sit together instead of scattering across the part. Time
    -- ranges prune on the partition. server_name is left out because the sorting
    -- key would need allow_nullable_key, which is off by default, and because it
    -- tracks the address closely.
    ORDER BY (team_id, sandbox_id, destination_ip, destination_port, last_seen)
    -- The same window as the log tables, to start. These rows are aggregated
    -- rather than per event, so a longer one is affordable once the read side
    -- shows what it needs.
    TTL toDateTime(ingested_at) + INTERVAL 7 DAY
    SETTINGS ttl_only_drop_parts = 1;
-- +goose StatementEnd

-- +goose StatementBegin
-- Sharded on the sandbox rather than on the team, unlike the neighbouring
-- tables: the workload that produces the most rows here is one team's sandboxes
-- contacting many endpoints, and sharding on the team would land all of it on a
-- single shard.
CREATE TABLE sandbox_egress AS sandbox_egress_local
    ENGINE = Distributed('cluster', currentDatabase(), 'sandbox_egress_local', xxHash64(sandbox_id));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS sandbox_egress;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS sandbox_egress_local;
-- +goose StatementEnd
