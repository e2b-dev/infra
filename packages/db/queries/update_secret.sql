-- name: UpdateSecret :execrows
UPDATE "public"."secrets"
SET
    label = @label,
    description = @description,
    updated_at = CURRENT_TIMESTAMP
WHERE id = @id
AND team_id = @team_id;

