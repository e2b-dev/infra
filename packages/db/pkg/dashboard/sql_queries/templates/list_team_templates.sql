-- Paginated list of a team's templates, optionally including default templates
-- (env_defaults membership) inline when include_defaults is true. Filtering
-- (cpu/memory/visibility) and name+id search are shared across every variant;
-- sorting is split into one query per (column, direction) with a fixed ORDER BY
-- and a keyset predicate keyed on the sort column + the env id tiebreak (always
-- ascending). The cursor columns are nullable: a NULL cursor means the first
-- page.

-- name: ListTeamTemplatesByNameAsc :many
SELECT
    e.id AS template_id,
    e.created_at,
    e.updated_at,
    e.public,
    e.build_count,
    e.spawn_count,
    e.last_spawned_at,
    e.created_by AS creator_id,
    COALESCE(eb.id, '00000000-0000-0000-0000-000000000000'::uuid) AS build_id,
    COALESCE(eb.vcpu, 0)::bigint AS cpu_count,
    COALESCE(eb.ram_mb, 0)::bigint AS memory_mb,
    eb.total_disk_size_mb AS disk_size_mb,
    eb.envd_version AS envd_version,
    COALESCE(ea.aliases, ARRAY[]::text[])::text[] AS aliases,
    COALESCE(ea.names, ARRAY[]::text[])::text[] AS names,
    (d.env_id IS NOT NULL)::boolean AS is_default,
    d.description AS default_description,
    COALESCE(ea.name_sort_key, '')::text AS name_sort_key
FROM public.envs AS e
LEFT JOIN public.env_defaults AS d ON d.env_id = e.id
LEFT JOIN LATERAL (
    SELECT
        ARRAY_AGG(alias ORDER BY alias) AS aliases,
        ARRAY_AGG(CASE WHEN namespace IS NOT NULL THEN namespace || '/' || alias ELSE alias END ORDER BY alias) AS names,
        MIN(CASE WHEN namespace IS NOT NULL THEN namespace || '/' || alias ELSE alias END) AS name_sort_key
    FROM public.env_aliases
    WHERE env_id = e.id
) ea ON TRUE
LEFT JOIN LATERAL (
    SELECT b.id, b.vcpu, b.ram_mb, b.total_disk_size_mb, b.envd_version
    FROM public.env_build_assignments AS ba
    JOIN public.env_builds AS b ON b.id = ba.build_id
    WHERE ba.env_id = e.id AND ba.tag = 'default' AND b.status_group = 'ready'
    ORDER BY ba.created_at DESC
    LIMIT 1
) eb ON TRUE
WHERE
    (
        (e.team_id = sqlc.arg(team_id)::uuid AND e.source = 'template')
        OR (sqlc.arg(include_defaults)::boolean AND d.env_id IS NOT NULL)
    )
    AND (sqlc.arg(cpu_count)::bigint = 0 OR COALESCE(eb.vcpu, 0) = sqlc.arg(cpu_count)::bigint)
    AND (sqlc.arg(memory_mb)::bigint = 0 OR COALESCE(eb.ram_mb, 0) = sqlc.arg(memory_mb)::bigint)
    AND (sqlc.arg(filter_public)::smallint = -1 OR e.public = (sqlc.arg(filter_public)::smallint = 1))
    AND (
        sqlc.arg(search)::text = ''
        OR e.id ILIKE '%' || sqlc.arg(search)::text || '%'
        OR EXISTS (
            SELECT 1 FROM public.env_aliases sa
            WHERE sa.env_id = e.id
              AND (
                sa.alias ILIKE '%' || sqlc.arg(search)::text || '%'
                OR COALESCE(sa.namespace, '') || '/' || sa.alias ILIKE '%' || sqlc.arg(search)::text || '%'
              )
        )
    )
    AND (
        sqlc.narg(cursor_name)::text IS NULL
        OR COALESCE(ea.name_sort_key, '') > sqlc.narg(cursor_name)::text
        OR (COALESCE(ea.name_sort_key, '') = sqlc.narg(cursor_name)::text AND e.id > sqlc.narg(cursor_id)::text)
    )
ORDER BY COALESCE(ea.name_sort_key, '') ASC, e.id ASC
LIMIT sqlc.arg(limit_plus_one)::int;

-- name: ListTeamTemplatesByNameDesc :many
SELECT
    e.id AS template_id,
    e.created_at,
    e.updated_at,
    e.public,
    e.build_count,
    e.spawn_count,
    e.last_spawned_at,
    e.created_by AS creator_id,
    COALESCE(eb.id, '00000000-0000-0000-0000-000000000000'::uuid) AS build_id,
    COALESCE(eb.vcpu, 0)::bigint AS cpu_count,
    COALESCE(eb.ram_mb, 0)::bigint AS memory_mb,
    eb.total_disk_size_mb AS disk_size_mb,
    eb.envd_version AS envd_version,
    COALESCE(ea.aliases, ARRAY[]::text[])::text[] AS aliases,
    COALESCE(ea.names, ARRAY[]::text[])::text[] AS names,
    (d.env_id IS NOT NULL)::boolean AS is_default,
    d.description AS default_description,
    COALESCE(ea.name_sort_key, '')::text AS name_sort_key
FROM public.envs AS e
LEFT JOIN public.env_defaults AS d ON d.env_id = e.id
LEFT JOIN LATERAL (
    SELECT
        ARRAY_AGG(alias ORDER BY alias) AS aliases,
        ARRAY_AGG(CASE WHEN namespace IS NOT NULL THEN namespace || '/' || alias ELSE alias END ORDER BY alias) AS names,
        MIN(CASE WHEN namespace IS NOT NULL THEN namespace || '/' || alias ELSE alias END) AS name_sort_key
    FROM public.env_aliases
    WHERE env_id = e.id
) ea ON TRUE
LEFT JOIN LATERAL (
    SELECT b.id, b.vcpu, b.ram_mb, b.total_disk_size_mb, b.envd_version
    FROM public.env_build_assignments AS ba
    JOIN public.env_builds AS b ON b.id = ba.build_id
    WHERE ba.env_id = e.id AND ba.tag = 'default' AND b.status_group = 'ready'
    ORDER BY ba.created_at DESC
    LIMIT 1
) eb ON TRUE
WHERE
    (
        (e.team_id = sqlc.arg(team_id)::uuid AND e.source = 'template')
        OR (sqlc.arg(include_defaults)::boolean AND d.env_id IS NOT NULL)
    )
    AND (sqlc.arg(cpu_count)::bigint = 0 OR COALESCE(eb.vcpu, 0) = sqlc.arg(cpu_count)::bigint)
    AND (sqlc.arg(memory_mb)::bigint = 0 OR COALESCE(eb.ram_mb, 0) = sqlc.arg(memory_mb)::bigint)
    AND (sqlc.arg(filter_public)::smallint = -1 OR e.public = (sqlc.arg(filter_public)::smallint = 1))
    AND (
        sqlc.arg(search)::text = ''
        OR e.id ILIKE '%' || sqlc.arg(search)::text || '%'
        OR EXISTS (
            SELECT 1 FROM public.env_aliases sa
            WHERE sa.env_id = e.id
              AND (
                sa.alias ILIKE '%' || sqlc.arg(search)::text || '%'
                OR COALESCE(sa.namespace, '') || '/' || sa.alias ILIKE '%' || sqlc.arg(search)::text || '%'
              )
        )
    )
    AND (
        sqlc.narg(cursor_name)::text IS NULL
        OR COALESCE(ea.name_sort_key, '') < sqlc.narg(cursor_name)::text
        OR (COALESCE(ea.name_sort_key, '') = sqlc.narg(cursor_name)::text AND e.id > sqlc.narg(cursor_id)::text)
    )
ORDER BY COALESCE(ea.name_sort_key, '') DESC, e.id ASC
LIMIT sqlc.arg(limit_plus_one)::int;

-- name: ListTeamTemplatesByCpuCountAsc :many
SELECT
    e.id AS template_id,
    e.created_at,
    e.updated_at,
    e.public,
    e.build_count,
    e.spawn_count,
    e.last_spawned_at,
    e.created_by AS creator_id,
    COALESCE(eb.id, '00000000-0000-0000-0000-000000000000'::uuid) AS build_id,
    COALESCE(eb.vcpu, 0)::bigint AS cpu_count,
    COALESCE(eb.ram_mb, 0)::bigint AS memory_mb,
    eb.total_disk_size_mb AS disk_size_mb,
    eb.envd_version AS envd_version,
    COALESCE(ea.aliases, ARRAY[]::text[])::text[] AS aliases,
    COALESCE(ea.names, ARRAY[]::text[])::text[] AS names,
    (d.env_id IS NOT NULL)::boolean AS is_default,
    d.description AS default_description,
    COALESCE(ea.name_sort_key, '')::text AS name_sort_key
FROM public.envs AS e
LEFT JOIN public.env_defaults AS d ON d.env_id = e.id
LEFT JOIN LATERAL (
    SELECT
        ARRAY_AGG(alias ORDER BY alias) AS aliases,
        ARRAY_AGG(CASE WHEN namespace IS NOT NULL THEN namespace || '/' || alias ELSE alias END ORDER BY alias) AS names,
        MIN(CASE WHEN namespace IS NOT NULL THEN namespace || '/' || alias ELSE alias END) AS name_sort_key
    FROM public.env_aliases
    WHERE env_id = e.id
) ea ON TRUE
LEFT JOIN LATERAL (
    SELECT b.id, b.vcpu, b.ram_mb, b.total_disk_size_mb, b.envd_version
    FROM public.env_build_assignments AS ba
    JOIN public.env_builds AS b ON b.id = ba.build_id
    WHERE ba.env_id = e.id AND ba.tag = 'default' AND b.status_group = 'ready'
    ORDER BY ba.created_at DESC
    LIMIT 1
) eb ON TRUE
WHERE
    (
        (e.team_id = sqlc.arg(team_id)::uuid AND e.source = 'template')
        OR (sqlc.arg(include_defaults)::boolean AND d.env_id IS NOT NULL)
    )
    AND (sqlc.arg(cpu_count)::bigint = 0 OR COALESCE(eb.vcpu, 0) = sqlc.arg(cpu_count)::bigint)
    AND (sqlc.arg(memory_mb)::bigint = 0 OR COALESCE(eb.ram_mb, 0) = sqlc.arg(memory_mb)::bigint)
    AND (sqlc.arg(filter_public)::smallint = -1 OR e.public = (sqlc.arg(filter_public)::smallint = 1))
    AND (
        sqlc.arg(search)::text = ''
        OR e.id ILIKE '%' || sqlc.arg(search)::text || '%'
        OR EXISTS (
            SELECT 1 FROM public.env_aliases sa
            WHERE sa.env_id = e.id
              AND (
                sa.alias ILIKE '%' || sqlc.arg(search)::text || '%'
                OR COALESCE(sa.namespace, '') || '/' || sa.alias ILIKE '%' || sqlc.arg(search)::text || '%'
              )
        )
    )
    AND (
        sqlc.narg(cursor_cpu_count)::bigint IS NULL
        OR COALESCE(eb.vcpu, 0) > sqlc.narg(cursor_cpu_count)::bigint
        OR (COALESCE(eb.vcpu, 0) = sqlc.narg(cursor_cpu_count)::bigint AND e.id > sqlc.narg(cursor_id)::text)
    )
ORDER BY COALESCE(eb.vcpu, 0) ASC, e.id ASC
LIMIT sqlc.arg(limit_plus_one)::int;

-- name: ListTeamTemplatesByCpuCountDesc :many
SELECT
    e.id AS template_id,
    e.created_at,
    e.updated_at,
    e.public,
    e.build_count,
    e.spawn_count,
    e.last_spawned_at,
    e.created_by AS creator_id,
    COALESCE(eb.id, '00000000-0000-0000-0000-000000000000'::uuid) AS build_id,
    COALESCE(eb.vcpu, 0)::bigint AS cpu_count,
    COALESCE(eb.ram_mb, 0)::bigint AS memory_mb,
    eb.total_disk_size_mb AS disk_size_mb,
    eb.envd_version AS envd_version,
    COALESCE(ea.aliases, ARRAY[]::text[])::text[] AS aliases,
    COALESCE(ea.names, ARRAY[]::text[])::text[] AS names,
    (d.env_id IS NOT NULL)::boolean AS is_default,
    d.description AS default_description,
    COALESCE(ea.name_sort_key, '')::text AS name_sort_key
FROM public.envs AS e
LEFT JOIN public.env_defaults AS d ON d.env_id = e.id
LEFT JOIN LATERAL (
    SELECT
        ARRAY_AGG(alias ORDER BY alias) AS aliases,
        ARRAY_AGG(CASE WHEN namespace IS NOT NULL THEN namespace || '/' || alias ELSE alias END ORDER BY alias) AS names,
        MIN(CASE WHEN namespace IS NOT NULL THEN namespace || '/' || alias ELSE alias END) AS name_sort_key
    FROM public.env_aliases
    WHERE env_id = e.id
) ea ON TRUE
LEFT JOIN LATERAL (
    SELECT b.id, b.vcpu, b.ram_mb, b.total_disk_size_mb, b.envd_version
    FROM public.env_build_assignments AS ba
    JOIN public.env_builds AS b ON b.id = ba.build_id
    WHERE ba.env_id = e.id AND ba.tag = 'default' AND b.status_group = 'ready'
    ORDER BY ba.created_at DESC
    LIMIT 1
) eb ON TRUE
WHERE
    (
        (e.team_id = sqlc.arg(team_id)::uuid AND e.source = 'template')
        OR (sqlc.arg(include_defaults)::boolean AND d.env_id IS NOT NULL)
    )
    AND (sqlc.arg(cpu_count)::bigint = 0 OR COALESCE(eb.vcpu, 0) = sqlc.arg(cpu_count)::bigint)
    AND (sqlc.arg(memory_mb)::bigint = 0 OR COALESCE(eb.ram_mb, 0) = sqlc.arg(memory_mb)::bigint)
    AND (sqlc.arg(filter_public)::smallint = -1 OR e.public = (sqlc.arg(filter_public)::smallint = 1))
    AND (
        sqlc.arg(search)::text = ''
        OR e.id ILIKE '%' || sqlc.arg(search)::text || '%'
        OR EXISTS (
            SELECT 1 FROM public.env_aliases sa
            WHERE sa.env_id = e.id
              AND (
                sa.alias ILIKE '%' || sqlc.arg(search)::text || '%'
                OR COALESCE(sa.namespace, '') || '/' || sa.alias ILIKE '%' || sqlc.arg(search)::text || '%'
              )
        )
    )
    AND (
        sqlc.narg(cursor_cpu_count)::bigint IS NULL
        OR COALESCE(eb.vcpu, 0) < sqlc.narg(cursor_cpu_count)::bigint
        OR (COALESCE(eb.vcpu, 0) = sqlc.narg(cursor_cpu_count)::bigint AND e.id > sqlc.narg(cursor_id)::text)
    )
ORDER BY COALESCE(eb.vcpu, 0) DESC, e.id ASC
LIMIT sqlc.arg(limit_plus_one)::int;

-- name: ListTeamTemplatesByMemoryMbAsc :many
SELECT
    e.id AS template_id,
    e.created_at,
    e.updated_at,
    e.public,
    e.build_count,
    e.spawn_count,
    e.last_spawned_at,
    e.created_by AS creator_id,
    COALESCE(eb.id, '00000000-0000-0000-0000-000000000000'::uuid) AS build_id,
    COALESCE(eb.vcpu, 0)::bigint AS cpu_count,
    COALESCE(eb.ram_mb, 0)::bigint AS memory_mb,
    eb.total_disk_size_mb AS disk_size_mb,
    eb.envd_version AS envd_version,
    COALESCE(ea.aliases, ARRAY[]::text[])::text[] AS aliases,
    COALESCE(ea.names, ARRAY[]::text[])::text[] AS names,
    (d.env_id IS NOT NULL)::boolean AS is_default,
    d.description AS default_description,
    COALESCE(ea.name_sort_key, '')::text AS name_sort_key
FROM public.envs AS e
LEFT JOIN public.env_defaults AS d ON d.env_id = e.id
LEFT JOIN LATERAL (
    SELECT
        ARRAY_AGG(alias ORDER BY alias) AS aliases,
        ARRAY_AGG(CASE WHEN namespace IS NOT NULL THEN namespace || '/' || alias ELSE alias END ORDER BY alias) AS names,
        MIN(CASE WHEN namespace IS NOT NULL THEN namespace || '/' || alias ELSE alias END) AS name_sort_key
    FROM public.env_aliases
    WHERE env_id = e.id
) ea ON TRUE
LEFT JOIN LATERAL (
    SELECT b.id, b.vcpu, b.ram_mb, b.total_disk_size_mb, b.envd_version
    FROM public.env_build_assignments AS ba
    JOIN public.env_builds AS b ON b.id = ba.build_id
    WHERE ba.env_id = e.id AND ba.tag = 'default' AND b.status_group = 'ready'
    ORDER BY ba.created_at DESC
    LIMIT 1
) eb ON TRUE
WHERE
    (
        (e.team_id = sqlc.arg(team_id)::uuid AND e.source = 'template')
        OR (sqlc.arg(include_defaults)::boolean AND d.env_id IS NOT NULL)
    )
    AND (sqlc.arg(cpu_count)::bigint = 0 OR COALESCE(eb.vcpu, 0) = sqlc.arg(cpu_count)::bigint)
    AND (sqlc.arg(memory_mb)::bigint = 0 OR COALESCE(eb.ram_mb, 0) = sqlc.arg(memory_mb)::bigint)
    AND (sqlc.arg(filter_public)::smallint = -1 OR e.public = (sqlc.arg(filter_public)::smallint = 1))
    AND (
        sqlc.arg(search)::text = ''
        OR e.id ILIKE '%' || sqlc.arg(search)::text || '%'
        OR EXISTS (
            SELECT 1 FROM public.env_aliases sa
            WHERE sa.env_id = e.id
              AND (
                sa.alias ILIKE '%' || sqlc.arg(search)::text || '%'
                OR COALESCE(sa.namespace, '') || '/' || sa.alias ILIKE '%' || sqlc.arg(search)::text || '%'
              )
        )
    )
    AND (
        sqlc.narg(cursor_memory_mb)::bigint IS NULL
        OR COALESCE(eb.ram_mb, 0) > sqlc.narg(cursor_memory_mb)::bigint
        OR (COALESCE(eb.ram_mb, 0) = sqlc.narg(cursor_memory_mb)::bigint AND e.id > sqlc.narg(cursor_id)::text)
    )
ORDER BY COALESCE(eb.ram_mb, 0) ASC, e.id ASC
LIMIT sqlc.arg(limit_plus_one)::int;

-- name: ListTeamTemplatesByMemoryMbDesc :many
SELECT
    e.id AS template_id,
    e.created_at,
    e.updated_at,
    e.public,
    e.build_count,
    e.spawn_count,
    e.last_spawned_at,
    e.created_by AS creator_id,
    COALESCE(eb.id, '00000000-0000-0000-0000-000000000000'::uuid) AS build_id,
    COALESCE(eb.vcpu, 0)::bigint AS cpu_count,
    COALESCE(eb.ram_mb, 0)::bigint AS memory_mb,
    eb.total_disk_size_mb AS disk_size_mb,
    eb.envd_version AS envd_version,
    COALESCE(ea.aliases, ARRAY[]::text[])::text[] AS aliases,
    COALESCE(ea.names, ARRAY[]::text[])::text[] AS names,
    (d.env_id IS NOT NULL)::boolean AS is_default,
    d.description AS default_description,
    COALESCE(ea.name_sort_key, '')::text AS name_sort_key
FROM public.envs AS e
LEFT JOIN public.env_defaults AS d ON d.env_id = e.id
LEFT JOIN LATERAL (
    SELECT
        ARRAY_AGG(alias ORDER BY alias) AS aliases,
        ARRAY_AGG(CASE WHEN namespace IS NOT NULL THEN namespace || '/' || alias ELSE alias END ORDER BY alias) AS names,
        MIN(CASE WHEN namespace IS NOT NULL THEN namespace || '/' || alias ELSE alias END) AS name_sort_key
    FROM public.env_aliases
    WHERE env_id = e.id
) ea ON TRUE
LEFT JOIN LATERAL (
    SELECT b.id, b.vcpu, b.ram_mb, b.total_disk_size_mb, b.envd_version
    FROM public.env_build_assignments AS ba
    JOIN public.env_builds AS b ON b.id = ba.build_id
    WHERE ba.env_id = e.id AND ba.tag = 'default' AND b.status_group = 'ready'
    ORDER BY ba.created_at DESC
    LIMIT 1
) eb ON TRUE
WHERE
    (
        (e.team_id = sqlc.arg(team_id)::uuid AND e.source = 'template')
        OR (sqlc.arg(include_defaults)::boolean AND d.env_id IS NOT NULL)
    )
    AND (sqlc.arg(cpu_count)::bigint = 0 OR COALESCE(eb.vcpu, 0) = sqlc.arg(cpu_count)::bigint)
    AND (sqlc.arg(memory_mb)::bigint = 0 OR COALESCE(eb.ram_mb, 0) = sqlc.arg(memory_mb)::bigint)
    AND (sqlc.arg(filter_public)::smallint = -1 OR e.public = (sqlc.arg(filter_public)::smallint = 1))
    AND (
        sqlc.arg(search)::text = ''
        OR e.id ILIKE '%' || sqlc.arg(search)::text || '%'
        OR EXISTS (
            SELECT 1 FROM public.env_aliases sa
            WHERE sa.env_id = e.id
              AND (
                sa.alias ILIKE '%' || sqlc.arg(search)::text || '%'
                OR COALESCE(sa.namespace, '') || '/' || sa.alias ILIKE '%' || sqlc.arg(search)::text || '%'
              )
        )
    )
    AND (
        sqlc.narg(cursor_memory_mb)::bigint IS NULL
        OR COALESCE(eb.ram_mb, 0) < sqlc.narg(cursor_memory_mb)::bigint
        OR (COALESCE(eb.ram_mb, 0) = sqlc.narg(cursor_memory_mb)::bigint AND e.id > sqlc.narg(cursor_id)::text)
    )
ORDER BY COALESCE(eb.ram_mb, 0) DESC, e.id ASC
LIMIT sqlc.arg(limit_plus_one)::int;

-- name: ListTeamTemplatesByCreatedAtAsc :many
SELECT
    e.id AS template_id,
    e.created_at,
    e.updated_at,
    e.public,
    e.build_count,
    e.spawn_count,
    e.last_spawned_at,
    e.created_by AS creator_id,
    COALESCE(eb.id, '00000000-0000-0000-0000-000000000000'::uuid) AS build_id,
    COALESCE(eb.vcpu, 0)::bigint AS cpu_count,
    COALESCE(eb.ram_mb, 0)::bigint AS memory_mb,
    eb.total_disk_size_mb AS disk_size_mb,
    eb.envd_version AS envd_version,
    COALESCE(ea.aliases, ARRAY[]::text[])::text[] AS aliases,
    COALESCE(ea.names, ARRAY[]::text[])::text[] AS names,
    (d.env_id IS NOT NULL)::boolean AS is_default,
    d.description AS default_description,
    COALESCE(ea.name_sort_key, '')::text AS name_sort_key
FROM public.envs AS e
LEFT JOIN public.env_defaults AS d ON d.env_id = e.id
LEFT JOIN LATERAL (
    SELECT
        ARRAY_AGG(alias ORDER BY alias) AS aliases,
        ARRAY_AGG(CASE WHEN namespace IS NOT NULL THEN namespace || '/' || alias ELSE alias END ORDER BY alias) AS names,
        MIN(CASE WHEN namespace IS NOT NULL THEN namespace || '/' || alias ELSE alias END) AS name_sort_key
    FROM public.env_aliases
    WHERE env_id = e.id
) ea ON TRUE
LEFT JOIN LATERAL (
    SELECT b.id, b.vcpu, b.ram_mb, b.total_disk_size_mb, b.envd_version
    FROM public.env_build_assignments AS ba
    JOIN public.env_builds AS b ON b.id = ba.build_id
    WHERE ba.env_id = e.id AND ba.tag = 'default' AND b.status_group = 'ready'
    ORDER BY ba.created_at DESC
    LIMIT 1
) eb ON TRUE
WHERE
    (
        (e.team_id = sqlc.arg(team_id)::uuid AND e.source = 'template')
        OR (sqlc.arg(include_defaults)::boolean AND d.env_id IS NOT NULL)
    )
    AND (sqlc.arg(cpu_count)::bigint = 0 OR COALESCE(eb.vcpu, 0) = sqlc.arg(cpu_count)::bigint)
    AND (sqlc.arg(memory_mb)::bigint = 0 OR COALESCE(eb.ram_mb, 0) = sqlc.arg(memory_mb)::bigint)
    AND (sqlc.arg(filter_public)::smallint = -1 OR e.public = (sqlc.arg(filter_public)::smallint = 1))
    AND (
        sqlc.arg(search)::text = ''
        OR e.id ILIKE '%' || sqlc.arg(search)::text || '%'
        OR EXISTS (
            SELECT 1 FROM public.env_aliases sa
            WHERE sa.env_id = e.id
              AND (
                sa.alias ILIKE '%' || sqlc.arg(search)::text || '%'
                OR COALESCE(sa.namespace, '') || '/' || sa.alias ILIKE '%' || sqlc.arg(search)::text || '%'
              )
        )
    )
    AND (
        sqlc.narg(cursor_created_at)::timestamptz IS NULL
        OR e.created_at > sqlc.narg(cursor_created_at)::timestamptz
        OR (e.created_at = sqlc.narg(cursor_created_at)::timestamptz AND e.id > sqlc.narg(cursor_id)::text)
    )
ORDER BY e.created_at ASC, e.id ASC
LIMIT sqlc.arg(limit_plus_one)::int;

-- name: ListTeamTemplatesByCreatedAtDesc :many
SELECT
    e.id AS template_id,
    e.created_at,
    e.updated_at,
    e.public,
    e.build_count,
    e.spawn_count,
    e.last_spawned_at,
    e.created_by AS creator_id,
    COALESCE(eb.id, '00000000-0000-0000-0000-000000000000'::uuid) AS build_id,
    COALESCE(eb.vcpu, 0)::bigint AS cpu_count,
    COALESCE(eb.ram_mb, 0)::bigint AS memory_mb,
    eb.total_disk_size_mb AS disk_size_mb,
    eb.envd_version AS envd_version,
    COALESCE(ea.aliases, ARRAY[]::text[])::text[] AS aliases,
    COALESCE(ea.names, ARRAY[]::text[])::text[] AS names,
    (d.env_id IS NOT NULL)::boolean AS is_default,
    d.description AS default_description,
    COALESCE(ea.name_sort_key, '')::text AS name_sort_key
FROM public.envs AS e
LEFT JOIN public.env_defaults AS d ON d.env_id = e.id
LEFT JOIN LATERAL (
    SELECT
        ARRAY_AGG(alias ORDER BY alias) AS aliases,
        ARRAY_AGG(CASE WHEN namespace IS NOT NULL THEN namespace || '/' || alias ELSE alias END ORDER BY alias) AS names,
        MIN(CASE WHEN namespace IS NOT NULL THEN namespace || '/' || alias ELSE alias END) AS name_sort_key
    FROM public.env_aliases
    WHERE env_id = e.id
) ea ON TRUE
LEFT JOIN LATERAL (
    SELECT b.id, b.vcpu, b.ram_mb, b.total_disk_size_mb, b.envd_version
    FROM public.env_build_assignments AS ba
    JOIN public.env_builds AS b ON b.id = ba.build_id
    WHERE ba.env_id = e.id AND ba.tag = 'default' AND b.status_group = 'ready'
    ORDER BY ba.created_at DESC
    LIMIT 1
) eb ON TRUE
WHERE
    (
        (e.team_id = sqlc.arg(team_id)::uuid AND e.source = 'template')
        OR (sqlc.arg(include_defaults)::boolean AND d.env_id IS NOT NULL)
    )
    AND (sqlc.arg(cpu_count)::bigint = 0 OR COALESCE(eb.vcpu, 0) = sqlc.arg(cpu_count)::bigint)
    AND (sqlc.arg(memory_mb)::bigint = 0 OR COALESCE(eb.ram_mb, 0) = sqlc.arg(memory_mb)::bigint)
    AND (sqlc.arg(filter_public)::smallint = -1 OR e.public = (sqlc.arg(filter_public)::smallint = 1))
    AND (
        sqlc.arg(search)::text = ''
        OR e.id ILIKE '%' || sqlc.arg(search)::text || '%'
        OR EXISTS (
            SELECT 1 FROM public.env_aliases sa
            WHERE sa.env_id = e.id
              AND (
                sa.alias ILIKE '%' || sqlc.arg(search)::text || '%'
                OR COALESCE(sa.namespace, '') || '/' || sa.alias ILIKE '%' || sqlc.arg(search)::text || '%'
              )
        )
    )
    AND (
        sqlc.narg(cursor_created_at)::timestamptz IS NULL
        OR e.created_at < sqlc.narg(cursor_created_at)::timestamptz
        OR (e.created_at = sqlc.narg(cursor_created_at)::timestamptz AND e.id > sqlc.narg(cursor_id)::text)
    )
ORDER BY e.created_at DESC, e.id ASC
LIMIT sqlc.arg(limit_plus_one)::int;

-- name: ListTeamTemplatesByUpdatedAtAsc :many
SELECT
    e.id AS template_id,
    e.created_at,
    e.updated_at,
    e.public,
    e.build_count,
    e.spawn_count,
    e.last_spawned_at,
    e.created_by AS creator_id,
    COALESCE(eb.id, '00000000-0000-0000-0000-000000000000'::uuid) AS build_id,
    COALESCE(eb.vcpu, 0)::bigint AS cpu_count,
    COALESCE(eb.ram_mb, 0)::bigint AS memory_mb,
    eb.total_disk_size_mb AS disk_size_mb,
    eb.envd_version AS envd_version,
    COALESCE(ea.aliases, ARRAY[]::text[])::text[] AS aliases,
    COALESCE(ea.names, ARRAY[]::text[])::text[] AS names,
    (d.env_id IS NOT NULL)::boolean AS is_default,
    d.description AS default_description,
    COALESCE(ea.name_sort_key, '')::text AS name_sort_key
FROM public.envs AS e
LEFT JOIN public.env_defaults AS d ON d.env_id = e.id
LEFT JOIN LATERAL (
    SELECT
        ARRAY_AGG(alias ORDER BY alias) AS aliases,
        ARRAY_AGG(CASE WHEN namespace IS NOT NULL THEN namespace || '/' || alias ELSE alias END ORDER BY alias) AS names,
        MIN(CASE WHEN namespace IS NOT NULL THEN namespace || '/' || alias ELSE alias END) AS name_sort_key
    FROM public.env_aliases
    WHERE env_id = e.id
) ea ON TRUE
LEFT JOIN LATERAL (
    SELECT b.id, b.vcpu, b.ram_mb, b.total_disk_size_mb, b.envd_version
    FROM public.env_build_assignments AS ba
    JOIN public.env_builds AS b ON b.id = ba.build_id
    WHERE ba.env_id = e.id AND ba.tag = 'default' AND b.status_group = 'ready'
    ORDER BY ba.created_at DESC
    LIMIT 1
) eb ON TRUE
WHERE
    (
        (e.team_id = sqlc.arg(team_id)::uuid AND e.source = 'template')
        OR (sqlc.arg(include_defaults)::boolean AND d.env_id IS NOT NULL)
    )
    AND (sqlc.arg(cpu_count)::bigint = 0 OR COALESCE(eb.vcpu, 0) = sqlc.arg(cpu_count)::bigint)
    AND (sqlc.arg(memory_mb)::bigint = 0 OR COALESCE(eb.ram_mb, 0) = sqlc.arg(memory_mb)::bigint)
    AND (sqlc.arg(filter_public)::smallint = -1 OR e.public = (sqlc.arg(filter_public)::smallint = 1))
    AND (
        sqlc.arg(search)::text = ''
        OR e.id ILIKE '%' || sqlc.arg(search)::text || '%'
        OR EXISTS (
            SELECT 1 FROM public.env_aliases sa
            WHERE sa.env_id = e.id
              AND (
                sa.alias ILIKE '%' || sqlc.arg(search)::text || '%'
                OR COALESCE(sa.namespace, '') || '/' || sa.alias ILIKE '%' || sqlc.arg(search)::text || '%'
              )
        )
    )
    AND (
        sqlc.narg(cursor_updated_at)::timestamptz IS NULL
        OR e.updated_at > sqlc.narg(cursor_updated_at)::timestamptz
        OR (e.updated_at = sqlc.narg(cursor_updated_at)::timestamptz AND e.id > sqlc.narg(cursor_id)::text)
    )
ORDER BY e.updated_at ASC, e.id ASC
LIMIT sqlc.arg(limit_plus_one)::int;

-- name: ListTeamTemplatesByUpdatedAtDesc :many
SELECT
    e.id AS template_id,
    e.created_at,
    e.updated_at,
    e.public,
    e.build_count,
    e.spawn_count,
    e.last_spawned_at,
    e.created_by AS creator_id,
    COALESCE(eb.id, '00000000-0000-0000-0000-000000000000'::uuid) AS build_id,
    COALESCE(eb.vcpu, 0)::bigint AS cpu_count,
    COALESCE(eb.ram_mb, 0)::bigint AS memory_mb,
    eb.total_disk_size_mb AS disk_size_mb,
    eb.envd_version AS envd_version,
    COALESCE(ea.aliases, ARRAY[]::text[])::text[] AS aliases,
    COALESCE(ea.names, ARRAY[]::text[])::text[] AS names,
    (d.env_id IS NOT NULL)::boolean AS is_default,
    d.description AS default_description,
    COALESCE(ea.name_sort_key, '')::text AS name_sort_key
FROM public.envs AS e
LEFT JOIN public.env_defaults AS d ON d.env_id = e.id
LEFT JOIN LATERAL (
    SELECT
        ARRAY_AGG(alias ORDER BY alias) AS aliases,
        ARRAY_AGG(CASE WHEN namespace IS NOT NULL THEN namespace || '/' || alias ELSE alias END ORDER BY alias) AS names,
        MIN(CASE WHEN namespace IS NOT NULL THEN namespace || '/' || alias ELSE alias END) AS name_sort_key
    FROM public.env_aliases
    WHERE env_id = e.id
) ea ON TRUE
LEFT JOIN LATERAL (
    SELECT b.id, b.vcpu, b.ram_mb, b.total_disk_size_mb, b.envd_version
    FROM public.env_build_assignments AS ba
    JOIN public.env_builds AS b ON b.id = ba.build_id
    WHERE ba.env_id = e.id AND ba.tag = 'default' AND b.status_group = 'ready'
    ORDER BY ba.created_at DESC
    LIMIT 1
) eb ON TRUE
WHERE
    (
        (e.team_id = sqlc.arg(team_id)::uuid AND e.source = 'template')
        OR (sqlc.arg(include_defaults)::boolean AND d.env_id IS NOT NULL)
    )
    AND (sqlc.arg(cpu_count)::bigint = 0 OR COALESCE(eb.vcpu, 0) = sqlc.arg(cpu_count)::bigint)
    AND (sqlc.arg(memory_mb)::bigint = 0 OR COALESCE(eb.ram_mb, 0) = sqlc.arg(memory_mb)::bigint)
    AND (sqlc.arg(filter_public)::smallint = -1 OR e.public = (sqlc.arg(filter_public)::smallint = 1))
    AND (
        sqlc.arg(search)::text = ''
        OR e.id ILIKE '%' || sqlc.arg(search)::text || '%'
        OR EXISTS (
            SELECT 1 FROM public.env_aliases sa
            WHERE sa.env_id = e.id
              AND (
                sa.alias ILIKE '%' || sqlc.arg(search)::text || '%'
                OR COALESCE(sa.namespace, '') || '/' || sa.alias ILIKE '%' || sqlc.arg(search)::text || '%'
              )
        )
    )
    AND (
        sqlc.narg(cursor_updated_at)::timestamptz IS NULL
        OR e.updated_at < sqlc.narg(cursor_updated_at)::timestamptz
        OR (e.updated_at = sqlc.narg(cursor_updated_at)::timestamptz AND e.id > sqlc.narg(cursor_id)::text)
    )
ORDER BY e.updated_at DESC, e.id ASC
LIMIT sqlc.arg(limit_plus_one)::int;

-- name: GetTeamTemplate :one
-- Single template lookup scoped to the team, optionally allowing default
-- templates (env_defaults membership) when include_defaults is true.
SELECT
    e.id AS template_id,
    e.created_at,
    e.updated_at,
    e.public,
    e.build_count,
    e.spawn_count,
    e.last_spawned_at,
    e.created_by AS creator_id,
    COALESCE(eb.id, '00000000-0000-0000-0000-000000000000'::uuid) AS build_id,
    COALESCE(eb.vcpu, 0)::bigint AS cpu_count,
    COALESCE(eb.ram_mb, 0)::bigint AS memory_mb,
    eb.total_disk_size_mb AS disk_size_mb,
    eb.envd_version AS envd_version,
    COALESCE(ea.aliases, ARRAY[]::text[])::text[] AS aliases,
    COALESCE(ea.names, ARRAY[]::text[])::text[] AS names,
    (d.env_id IS NOT NULL)::boolean AS is_default,
    d.description AS default_description,
    COALESCE(ea.name_sort_key, '')::text AS name_sort_key
FROM public.envs AS e
LEFT JOIN public.env_defaults AS d ON d.env_id = e.id
LEFT JOIN LATERAL (
    SELECT
        ARRAY_AGG(alias ORDER BY alias) AS aliases,
        ARRAY_AGG(CASE WHEN namespace IS NOT NULL THEN namespace || '/' || alias ELSE alias END ORDER BY alias) AS names,
        MIN(CASE WHEN namespace IS NOT NULL THEN namespace || '/' || alias ELSE alias END) AS name_sort_key
    FROM public.env_aliases
    WHERE env_id = e.id
) ea ON TRUE
LEFT JOIN LATERAL (
    SELECT b.id, b.vcpu, b.ram_mb, b.total_disk_size_mb, b.envd_version
    FROM public.env_build_assignments AS ba
    JOIN public.env_builds AS b ON b.id = ba.build_id
    WHERE ba.env_id = e.id AND ba.tag = 'default' AND b.status_group = 'ready'
    ORDER BY ba.created_at DESC
    LIMIT 1
) eb ON TRUE
WHERE
    e.id = sqlc.arg(template_id)::text
    AND (
        (e.team_id = sqlc.arg(team_id)::uuid AND e.source = 'template')
        OR (sqlc.arg(include_defaults)::boolean AND d.env_id IS NOT NULL)
    )
LIMIT 1;
