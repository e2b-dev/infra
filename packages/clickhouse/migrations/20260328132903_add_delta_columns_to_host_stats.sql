-- +goose Up
-- +goose StatementBegin
ALTER TABLE sandbox_host_stats_local
	ADD COLUMN IF NOT EXISTS delta_cgroup_cpu_usage_usec UInt64 DEFAULT 0 CODEC (ZSTD(1)),
	ADD COLUMN IF NOT EXISTS delta_cgroup_cpu_user_usec UInt64 DEFAULT 0 CODEC (ZSTD(1)),
	ADD COLUMN IF NOT EXISTS delta_cgroup_cpu_system_usec UInt64 DEFAULT 0 CODEC (ZSTD(1)),
	ADD COLUMN IF NOT EXISTS interval_us UInt64 DEFAULT 0 CODEC (ZSTD(1));
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE sandbox_host_stats
	ADD COLUMN IF NOT EXISTS delta_cgroup_cpu_usage_usec UInt64 DEFAULT 0 CODEC (ZSTD(1)),
	ADD COLUMN IF NOT EXISTS delta_cgroup_cpu_user_usec UInt64 DEFAULT 0 CODEC (ZSTD(1)),
	ADD COLUMN IF NOT EXISTS delta_cgroup_cpu_system_usec UInt64 DEFAULT 0 CODEC (ZSTD(1)),
	ADD COLUMN IF NOT EXISTS interval_us UInt64 DEFAULT 0 CODEC (ZSTD(1));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sandbox_host_stats
	DROP COLUMN IF EXISTS delta_cgroup_cpu_usage_usec,
	DROP COLUMN IF EXISTS delta_cgroup_cpu_user_usec,
	DROP COLUMN IF EXISTS delta_cgroup_cpu_system_usec,
	DROP COLUMN IF EXISTS interval_us;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE sandbox_host_stats_local
	DROP COLUMN IF EXISTS delta_cgroup_cpu_usage_usec,
	DROP COLUMN IF EXISTS delta_cgroup_cpu_user_usec,
	DROP COLUMN IF EXISTS delta_cgroup_cpu_system_usec,
	DROP COLUMN IF EXISTS interval_us;
-- +goose StatementEnd
