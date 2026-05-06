-- name: GetTemplateIDByBuildID :one
-- Returns the template (env) id that owns the given build, picking the
-- most recent assignment if multiple exist (e.g. a tag was reassigned).
-- Used by the skipIfUnchanged short-circuit to look up the prior
-- snapshot's template id without re-running the full upsert. See
-- issue e2b-dev/infra#2580.
SELECT env_id
FROM public.env_build_assignments
WHERE build_id = sqlc.arg(build_id)::uuid
ORDER BY created_at DESC, id DESC
LIMIT 1;
