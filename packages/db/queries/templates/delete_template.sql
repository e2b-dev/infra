-- name: SoftDeleteTemplate :one
-- Step 1 of soft delete (run in a tx). The UPDATE locks the env row, so a
-- concurrent build registration holding that lock commits first; this then
-- soft-deletes. Returns the id (no row if already deleted or not owned). The
-- alias/active-build cleanup runs as separate statements afterwards so their
-- fresh snapshots see rows that racing registration committed during the wait.
UPDATE public.envs
SET deleted_at = NOW(), updated_at = NOW()
WHERE id = @template_id AND team_id = @team_id AND deleted_at IS NULL
RETURNING id;

-- name: ReleaseTemplateAliases :many
-- Releases the env's aliases (so the name is reusable) and returns their cache keys.
DELETE FROM public.env_aliases
WHERE env_id = @template_id
RETURNING (CASE WHEN namespace IS NOT NULL THEN namespace || '/' || alias ELSE alias END)::text AS alias_key;

-- name: DeleteActiveTemplateBuilds :exec
-- Clears in-flight build tracking (active_template_builds no longer cascades
-- since the env row is kept) so a deleted template stops counting toward the
-- team build concurrency limit.
DELETE FROM public.active_template_builds WHERE template_id = @template_id;
