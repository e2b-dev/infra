-- +goose Up
-- Insert-mostly table: dead-tuple triggers never fire here, so the
-- INSERT-driven autovacuum threshold is the one that matters. The default
-- fraction lets a large table go a long time between passes, leaving the
-- visibility map and planner statistics stale for scan-heavy access. Same
-- per-table override family as env_builds (20260723030000). Scheduling-only
-- storage-parameter change; brief ShareUpdateExclusive lock, no rewrite.
ALTER TABLE public.env_build_assignments SET (
    autovacuum_vacuum_insert_scale_factor = 0.02,
    autovacuum_analyze_scale_factor = 0.01
);

-- +goose Down
ALTER TABLE public.env_build_assignments RESET (
    autovacuum_vacuum_insert_scale_factor,
    autovacuum_analyze_scale_factor
);
