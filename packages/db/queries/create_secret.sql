-- name: CreateSecret :one
INSERT INTO "public"."secrets"(
    id,
    team_id,
    label,
    description,
    allowlist
)
VALUES (
    @id,
    @team_id,
    @label,
    @description,
    @allowlist
) RETURNING id, team_id, label, description, allowlist, created_at;

