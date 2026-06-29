-- name: MarkExclusiveTemplateBuildsDeleted :exec
-- Soft-deletes builds that are ONLY assigned to this template by setting their
-- status to 'deleted'. The env_builds rows (and their team/env attribution) are
-- preserved so a future GC can reclaim the storage. Builds shared with other
-- templates, and builds already marked deleted, are left untouched.
UPDATE "public"."env_builds" eb
SET status = 'deleted',
    reason = COALESCE(NULLIF(eb.reason, '{}'::jsonb), @reason),
    updated_at = NOW(),
    finished_at = COALESCE(eb.finished_at, NOW())
FROM "public"."env_build_assignments" eba
WHERE eba.build_id = eb.id
    AND eba.env_id = @template_id
    AND eb.status_group <> 'deleted'
    AND NOT EXISTS (
        SELECT 1 FROM "public"."env_build_assignments" other_eba
        WHERE other_eba.build_id = eb.id AND other_eba.env_id != @template_id
    );
