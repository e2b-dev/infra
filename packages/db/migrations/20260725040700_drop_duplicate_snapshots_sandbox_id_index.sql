-- +goose Up
-- +goose NO TRANSACTION

-- DROP only waits on in-flight lock holders; bound the wait so it neither
-- hangs unbounded nor dies to a short session default.
SET statement_timeout = '1h';

-- Exact duplicate of snapshots_sandbox_id_unique (same single column, same
-- order): the planner always has the unique index available, and this copy
-- recorded 0 lifetime scans in pg_stat_user_indexes while every snapshot
-- write paid its ~6 GiB maintenance tax.
DROP INDEX CONCURRENTLY IF EXISTS public.idx_snapshots_sandbox_id;

-- The migrator session is reused for subsequent migrations: restore its
-- baseline (scripts/migrator.go sets 3h per connection; RESET would clear
-- to the unbounded server default instead).
SET statement_timeout = '3h';

-- +goose Down
-- +goose NO TRANSACTION

-- A concurrent rebuild on a table this size can run for hours, and a
-- mid-build timeout would strand an INVALID index. Rolling back is a
-- deliberate manual operation with an operator present — unbounded on
-- purpose.
SET statement_timeout = 0;

-- An interrupted CONCURRENTLY build leaves an INVALID index that IF NOT
-- EXISTS would then skip forever — clear any leftover before rebuilding.
DROP INDEX CONCURRENTLY IF EXISTS public.idx_snapshots_sandbox_id;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_snapshots_sandbox_id
    ON public.snapshots (sandbox_id);

-- Restore the migrator session baseline (see Up).
SET statement_timeout = '3h';
