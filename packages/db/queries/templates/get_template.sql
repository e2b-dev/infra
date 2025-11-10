-- name: GetTemplateByIdOrAlias :one
SELECT e.* FROM "public"."envs" e
LEFT JOIN "public"."env_aliases" ea ON ea.env_id = e.id
WHERE (
  e.id = @template_id_or_alias OR
  ea.alias = @template_id_or_alias
);
