-- name: GetRowLevelSecurity :many
select
    c.relkind::text as kind,
    n.nspname as namespace_name,
    c.relname as table_name,
    c.relrowsecurity as row_level_security,
    c.reloptions::text[] as options
from pg_catalog.pg_class c
join pg_catalog.pg_namespace n on c.relnamespace = n.oid
where c.relkind in (
    'r', -- ordinary table
    'v', -- view
    'm'  -- materialized view
);

-- name: InsertTestTeam :exec
-- Inserts a team for tests with explicit id/slug. Uses the default 'base_v1'
-- tier created in migrations.
INSERT INTO public.teams (id, name, tier, email, slug)
VALUES (
    sqlc.arg(id)::uuid,
    sqlc.arg(name)::text,
    sqlc.arg(tier)::text,
    sqlc.arg(email)::text,
    sqlc.arg(slug)::text
);

-- name: InsertTestEnv :exec
-- Inserts an env (template) row used as a base for tests. Mirrors what
-- production code does via CreateOrUpdateTemplate but without the build_count
-- bookkeeping so tests can seed deterministic rows.
INSERT INTO public.envs (id, team_id, public, updated_at, source)
VALUES (
    sqlc.arg(id)::text,
    sqlc.arg(team_id)::uuid,
    sqlc.arg(public)::boolean,
    NOW(),
    sqlc.arg(source)::text
);

-- name: InsertTestEnvBuild :exec
-- Inserts an env_build row for tests with status 'ready'.
INSERT INTO public.env_builds (id, updated_at, status, vcpu, ram_mb, free_disk_size_mb, env_id, status_group, kernel_version, firecracker_version)
VALUES (
    sqlc.arg(id)::uuid,
    NOW(),
    'ready',
    sqlc.arg(vcpu)::bigint,
    sqlc.arg(ram_mb)::bigint,
    sqlc.arg(free_disk_size_mb)::bigint,
    sqlc.arg(env_id)::text,
    'ready',
    'vmlinux-6.1.102',
    'v1.10.1'
);

-- name: InsertTestEnvBuildAssignment :exec
-- Inserts an env_build_assignment row linking an env to a build with a tag.
INSERT INTO public.env_build_assignments (env_id, build_id, tag, source)
VALUES (
    sqlc.arg(env_id)::text,
    sqlc.arg(build_id)::uuid,
    sqlc.arg(tag)::text,
    'app'
);

-- name: InsertTestSnapshot :exec
-- Inserts a snapshot row for tests.
INSERT INTO public.snapshots (env_id, sandbox_id, base_env_id, sandbox_started_at, team_id, origin_node_id)
VALUES (
    sqlc.arg(env_id)::text,
    sqlc.arg(sandbox_id)::text,
    sqlc.arg(base_env_id)::text,
    NOW(),
    sqlc.arg(team_id)::uuid,
    'test-node'
);
