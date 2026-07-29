-- UpsertProjectLimits records a project's effective limits, which the
-- team_limits view reads in preference to the tier-plus-addons arithmetic.
--
-- Every column is supplied on every call: the caller sends a complete set, so
-- there is no partial update to merge and no prior row to read first. That
-- makes a retry of the same push a no-op rather than a second edit.
--
-- Returns nothing. A caller that names a team which does not exist gets a
-- foreign key violation, which the handler turns into a 404 -- the team is the
-- only thing that could be missing.
-- name: UpsertProjectLimits :exec
INSERT INTO public.project_limits (
    team_id,
    max_length_hours,
    concurrent_sandboxes,
    concurrent_template_builds,
    max_vcpu,
    max_ram_mb,
    disk_mb,
    events_ttl_days,
    default_free_disk_size_mb,
    max_disk_size_mb,
    updated_at
) VALUES (
    sqlc.arg(team_id)::uuid,
    sqlc.arg(max_length_hours)::bigint,
    sqlc.arg(concurrent_sandboxes)::bigint,
    sqlc.arg(concurrent_template_builds)::bigint,
    sqlc.arg(max_vcpu)::bigint,
    sqlc.arg(max_ram_mb)::bigint,
    sqlc.arg(disk_mb)::bigint,
    sqlc.arg(events_ttl_days)::bigint,
    sqlc.arg(default_free_disk_size_mb)::bigint,
    sqlc.arg(max_disk_size_mb)::bigint,
    now()
)
ON CONFLICT (team_id) DO UPDATE SET
    max_length_hours           = EXCLUDED.max_length_hours,
    concurrent_sandboxes       = EXCLUDED.concurrent_sandboxes,
    concurrent_template_builds = EXCLUDED.concurrent_template_builds,
    max_vcpu                   = EXCLUDED.max_vcpu,
    max_ram_mb                 = EXCLUDED.max_ram_mb,
    disk_mb                    = EXCLUDED.disk_mb,
    events_ttl_days            = EXCLUDED.events_ttl_days,
    default_free_disk_size_mb  = EXCLUDED.default_free_disk_size_mb,
    max_disk_size_mb           = EXCLUDED.max_disk_size_mb,
    updated_at                 = now();
