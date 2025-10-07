-- name: DeleteSecret :execrows
DELETE FROM "public"."secrets"
WHERE id = @id
AND team_id = @team_id;

