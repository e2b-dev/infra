-- Cursor-paginated team template listing for the CLI (GET /v2/templates).
--
-- Merges two keyset-paginated branches: the team's own templates and the
-- globally shared default templates (env_defaults, included when
-- include_defaults is set). Both branches compute identical sort keys and
-- cursor predicates and carry their own ORDER BY + LIMIT, so each is an
-- index-driven top-N scan and UNION dedups a team's own template that is also a
-- global default. Ordered created_at DESC (newest first) to match sandbox
-- pagination. The build columns and build_status are resolved per returned row
-- via the laterals below (latest default-tag build for status, latest ready
-- default-tag build for displayed resources), mirroring GetTeamTemplates.

-- name: GetTeamTemplatesWithCursor :many
WITH team_templates AS (
    SELECT
        e.id,
        e.created_at,
        e.updated_at,
        e.public,
        e.build_count,
        e.spawn_count,
        e.last_spawned_at,
        e.created_by,
        d.env_id AS default_env_id,
        d.description AS default_description
    FROM public.envs AS e
    LEFT JOIN public.env_defaults AS d ON d.env_id = e.id
    WHERE
        e.team_id = sqlc.arg(team_id)::uuid AND e.source = 'template'
        AND (e.created_at, e.id) < (sqlc.arg(cursor_created_at)::timestamptz, sqlc.arg(cursor_id)::text)
    ORDER BY e.created_at DESC, e.id DESC
    LIMIT sqlc.arg(limit_plus_one)::int
),
default_templates AS (
    SELECT
        e.id,
        e.created_at,
        e.updated_at,
        e.public,
        e.build_count,
        e.spawn_count,
        e.last_spawned_at,
        e.created_by,
        d.env_id AS default_env_id,
        d.description AS default_description
    FROM public.envs AS e
    JOIN public.env_defaults AS d ON d.env_id = e.id
    WHERE
        sqlc.arg(include_defaults)::boolean
        AND (e.created_at, e.id) < (sqlc.arg(cursor_created_at)::timestamptz, sqlc.arg(cursor_id)::text)
    ORDER BY e.created_at DESC, e.id DESC
    LIMIT sqlc.arg(limit_plus_one)::int
),
ranked AS (
    SELECT
        id, created_at, updated_at, public, build_count, spawn_count,
        last_spawned_at, created_by, default_env_id, default_description
    FROM team_templates
    UNION
    SELECT
        id, created_at, updated_at, public, build_count, spawn_count,
        last_spawned_at, created_by, default_env_id, default_description
    FROM default_templates
    ORDER BY created_at DESC, id DESC
    LIMIT sqlc.arg(limit_plus_one)::int
)
SELECT
        r.id AS template_id,
        r.created_at,
        r.updated_at,
        r.public,
        r.build_count,
        r.spawn_count,
        r.last_spawned_at,
        r.created_by AS creator_id,
        COALESCE(eb.id, '00000000-0000-0000-0000-000000000000'::uuid) AS build_id,
        COALESCE(eb.vcpu, 0)::bigint AS build_vcpu,
        COALESCE(eb.ram_mb, 0)::bigint AS build_ram_mb,
        eb.total_disk_size_mb AS build_total_disk_size_mb,
        eb.envd_version AS build_envd_version,
        COALESCE(latest_build.status_group, 'pending') AS build_status,
        COALESCE(ea.aliases, ARRAY[]::text[])::text[] AS aliases,
        COALESCE(ea.names, ARRAY[]::text[])::text[] AS names,
        (r.default_env_id IS NOT NULL)::boolean AS is_default,
        r.default_description
FROM ranked r
LEFT JOIN LATERAL (
    SELECT
        ARRAY_AGG(alias ORDER BY alias) AS aliases,
        ARRAY_AGG(CASE WHEN namespace IS NOT NULL THEN namespace || '/' || alias ELSE alias END ORDER BY alias) AS names
    FROM public.env_aliases
    WHERE env_id = r.id
) ea ON TRUE
LEFT JOIN LATERAL (
    SELECT b.status_group
    FROM public.env_build_assignments AS ba
    JOIN public.env_builds AS b ON b.id = ba.build_id
    WHERE ba.env_id = r.id AND ba.tag = 'default'
    ORDER BY ba.created_at DESC
    LIMIT 1
) latest_build ON TRUE
LEFT JOIN LATERAL (
    SELECT b.id, b.vcpu, b.ram_mb, b.total_disk_size_mb, b.envd_version
    FROM public.env_build_assignments AS ba
    JOIN public.env_builds AS b ON b.id = ba.build_id
    WHERE ba.env_id = r.id AND ba.tag = 'default' AND b.status_group = 'ready'
    ORDER BY ba.created_at DESC
    LIMIT 1
) eb ON TRUE
ORDER BY r.created_at DESC, r.id DESC;
