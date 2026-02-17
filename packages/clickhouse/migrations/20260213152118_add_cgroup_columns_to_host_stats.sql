-- +goose Up
-- +goose StatementBegin
ALTER TABLE sandbox_host_stats_local
	ADD COLUMN IF NOT EXISTS cgroup_cpu_usage_usec UInt64 DEFAULT 0 CODEC (ZSTD(1)),
	ADD COLUMN IF NOT EXISTS cgroup_cpu_user_usec UInt64 DEFAULT 0 CODEC (ZSTD(1)),
	ADD COLUMN IF NOT EXISTS cgroup_cpu_system_usec UInt64 DEFAULT 0 CODEC (ZSTD(1)),
	ADD COLUMN IF NOT EXISTS cgroup_memory_usage_bytes UInt64 DEFAULT 0 CODEC (ZSTD(1)),
	ADD COLUMN IF NOT EXISTS cgroup_memory_peak_bytes UInt64 DEFAULT 0 CODEC (ZSTD(1));
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE sandbox_host_stats
	ADD COLUMN IF NOT EXISTS cgroup_cpu_usage_usec UInt64 DEFAULT 0 CODEC (ZSTD(1)),
	ADD COLUMN IF NOT EXISTS cgroup_cpu_user_usec UInt64 DEFAULT 0 CODEC (ZSTD(1)),
	ADD COLUMN IF NOT EXISTS cgroup_cpu_system_usec UInt64 DEFAULT 0 CODEC (ZSTD(1)),
	ADD COLUMN IF NOT EXISTS cgroup_memory_usage_bytes UInt64 DEFAULT 0 CODEC (ZSTD(1)),
	ADD COLUMN IF NOT EXISTS cgroup_memory_peak_bytes UInt64 DEFAULT 0 CODEC (ZSTD(1));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sandbox_host_stats
	DROP COLUMN IF EXISTS cgroup_cpu_usage_usec,
	DROP COLUMN IF EXISTS cgroup_cpu_user_usec,
	DROP COLUMN IF EXISTS cgroup_cpu_system_usec,
	DROP COLUMN IF EXISTS cgroup_memory_usage_bytes,
	DROP COLUMN IF EXISTS cgroup_memory_peak_bytes;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE sandbox_host_stats_local
	DROP COLUMN IF EXISTS cgroup_cpu_usage_usec,
	DROP COLUMN IF EXISTS cgroup_cpu_user_usec,
	DROP COLUMN IF EXISTS cgroup_cpu_system_usec,
	DROP COLUMN IF EXISTS cgroup_memory_usage_bytes,
	DROP COLUMN IF EXISTS cgroup_memory_peak_bytes;
-- +goose StatementEnd
