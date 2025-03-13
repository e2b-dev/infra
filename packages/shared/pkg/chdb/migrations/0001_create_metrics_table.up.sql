CREATE TABLE IF NOT EXISTS default.metrics (
	timestamp DateTime('UTC'),
	sandbox_id String,
	team_id String,
	cpu_count UInt32,
	cpu_used_pct Float32,
	mem_total_mib UInt64,
	mem_used_mib UInt64
) Engine ReplacingMergeTree()
ORDER BY (timestamp)
PRIMARY KEY timestamp;
