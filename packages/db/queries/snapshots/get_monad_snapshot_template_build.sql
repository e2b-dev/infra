-- name: GetMonadSnapshotTemplateBuild :one
-- Resolve the immutable build/tag recorded when an unnamed reusable snapshot
-- template was created. The snapshot_templates row is the durable lineage edge;
-- env_build_assignments proves that the recorded build belongs to that template.
SELECT
    e.id AS template_id,
    e.team_id,
    st.build_id,
    eba.tag,
    eb.status_group
FROM "public"."active_envs" e
JOIN "public"."snapshot_templates" st
    ON st.env_id = e.id
JOIN "public"."env_build_assignments" eba
    ON eba.env_id = e.id
    AND eba.build_id = st.build_id
JOIN "public"."env_builds" eb
    ON eb.id = st.build_id
WHERE e.id = @template_id
  AND e.team_id = @team_id
  AND e.source = 'snapshot_template';
