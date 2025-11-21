-- name: CreateTemplateAlias :exec
INSERT INTO "public"."env_aliases" (alias, env_id, is_renamable)
VALUES (@alias, @template_id, TRUE);