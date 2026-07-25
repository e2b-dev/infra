-- +goose Up
-- +goose NO TRANSACTION

-- DROP only waits on in-flight lock holders; bound the wait so it neither
-- hangs unbounded nor dies to a short session default.
SET statement_timeout = '1h';

-- Plain btree on the raw status column: 10 lifetime scans in
-- pg_stat_user_indexes against ~3.6 GiB of size, while every build
-- insert/supersede pays its maintenance write. Status-filtered access runs
-- on the status_group partial indexes instead. env_builds is the single
-- most write-amplified table (0% HOT updates, 8 indexes), so each dropped
-- index cuts real WAL and dead-tuple volume.
DROP INDEX CONCURRENTLY IF EXISTS public.idx_env_builds_status;

-- +goose Down
-- +goose NO TRANSACTION

-- A concurrent rebuild on a ~330M row table runs for hours, and a mid-build
-- timeout would strand an INVALID index. Rolling back is a deliberate manual
-- operation with an operator present — unbounded on purpose.
SET statement_timeout = 0;

-- An interrupted CONCURRENTLY build leaves an INVALID index that IF NOT
-- EXISTS would then skip forever — clear any leftover before rebuilding.
DROP INDEX CONCURRENTLY IF EXISTS public.idx_env_builds_status;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_env_builds_status
    ON public.env_builds (status);
