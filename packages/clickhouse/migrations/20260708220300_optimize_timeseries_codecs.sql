-- Optimize storage codecs for time-series columns.
--
-- Timestamps and monotonic cumulative counters compress well with Delta+ZSTD;
-- Float64 metric values compress well with Gorilla+ZSTD. Codecs only apply to
-- newly written/merged parts, so this is a metadata-only change on the storage
-- (_local) tables and does not rewrite existing data. Codec-only MODIFY COLUMN
-- (no type/default repeated) avoids drift with the original definitions.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE team_metrics_gauge_local
    MODIFY COLUMN timestamp CODEC (Delta, ZSTD(1)),
    MODIFY COLUMN value CODEC (Gorilla, ZSTD(1));
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE team_metrics_sum_local
    MODIFY COLUMN timestamp CODEC (Delta, ZSTD(1)),
    MODIFY COLUMN value CODEC (Gorilla, ZSTD(1));
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE sandbox_metrics_gauge_local
    MODIFY COLUMN timestamp CODEC (Delta, ZSTD(1)),
    MODIFY COLUMN value CODEC (Gorilla, ZSTD(1));
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE sandbox_host_stats_local
    MODIFY COLUMN firecracker_cpu_user_time CODEC (Gorilla, ZSTD(1)),
    MODIFY COLUMN firecracker_cpu_system_time CODEC (Gorilla, ZSTD(1)),
    MODIFY COLUMN cgroup_cpu_usage_usec CODEC (Delta, ZSTD(1)),
    MODIFY COLUMN cgroup_cpu_user_usec CODEC (Delta, ZSTD(1)),
    MODIFY COLUMN cgroup_cpu_system_usec CODEC (Delta, ZSTD(1));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE team_metrics_gauge_local
    MODIFY COLUMN timestamp CODEC (ZSTD(1)),
    MODIFY COLUMN value CODEC (ZSTD(1));
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE team_metrics_sum_local
    MODIFY COLUMN timestamp CODEC (ZSTD(1)),
    MODIFY COLUMN value CODEC (ZSTD(1));
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE sandbox_metrics_gauge_local
    MODIFY COLUMN timestamp CODEC (ZSTD(1)),
    MODIFY COLUMN value CODEC (ZSTD(1));
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE sandbox_host_stats_local
    MODIFY COLUMN firecracker_cpu_user_time CODEC (ZSTD(1)),
    MODIFY COLUMN firecracker_cpu_system_time CODEC (ZSTD(1)),
    MODIFY COLUMN cgroup_cpu_usage_usec CODEC (ZSTD(1)),
    MODIFY COLUMN cgroup_cpu_user_usec CODEC (ZSTD(1)),
    MODIFY COLUMN cgroup_cpu_system_usec CODEC (ZSTD(1));
-- +goose StatementEnd
