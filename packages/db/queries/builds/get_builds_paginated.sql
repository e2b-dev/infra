-- Builds list, newest first, with keyset pagination, optional search, and the
-- optional cpu/memory resource filters.
--
--   * filter_build_id: exact build id (set when the search term is a UUID).
--   * name_search: exact template id OR case-insensitive substring on the
--     displayed template's alias / `namespace/alias` (set otherwise).
--   * cpu_count / memory_mb args: 0 means "unfiltered".
--   * cursor_* nargs: NULL means "first page".

-- name: GetTeamBuildsPage :many
SELECT
  b.id,
  b.status_group,
  b.reason,
  b.created_at,
  b.finished_at,
  b.vcpu,
  b.ram_mb,
  b.total_disk_size_mb,
  b.envd_version,
  eba.env_id AS template_id,
  COALESCE(ea.alias, '') AS template_alias
FROM public.env_builds b
JOIN LATERAL (
  SELECT a.env_id
  FROM public.env_build_assignments a
  WHERE a.build_id = b.id
  ORDER BY a.created_at DESC, a.id DESC
  LIMIT 1
) eba ON TRUE
LEFT JOIN LATERAL (
  SELECT x.alias
  FROM public.env_aliases x
  WHERE x.env_id = eba.env_id
  ORDER BY x.alias ASC
  LIMIT 1
) ea ON TRUE
WHERE b.team_id = sqlc.arg(team_id)::uuid
  AND b.status_group = ANY(sqlc.arg(statuses)::text[])
  AND (sqlc.narg(filter_build_id)::uuid IS NULL OR b.id = sqlc.narg(filter_build_id)::uuid)
  AND (
    sqlc.narg(name_search)::text IS NULL
    OR eba.env_id = sqlc.narg(name_search)::text
    OR EXISTS (
      SELECT 1 FROM public.env_aliases af
      WHERE af.env_id = eba.env_id
        AND (
          af.alias ILIKE '%' || sqlc.narg(name_search)::text || '%'
          OR COALESCE(af.namespace, '') || '/' || af.alias ILIKE '%' || sqlc.narg(name_search)::text || '%'
        )
    )
  )
  AND (sqlc.arg(cpu_count)::bigint = 0 OR COALESCE(b.vcpu, 0) = sqlc.arg(cpu_count)::bigint)
  AND (sqlc.arg(memory_mb)::bigint = 0 OR COALESCE(b.ram_mb, 0) = sqlc.arg(memory_mb)::bigint)
  AND (
    sqlc.narg(cursor_created_at)::timestamptz IS NULL
    OR (b.created_at, b.id) < (sqlc.narg(cursor_created_at)::timestamptz, sqlc.narg(cursor_id)::uuid)
  )
ORDER BY b.created_at DESC, b.id DESC
LIMIT sqlc.arg(limit_plus_one)::int;
