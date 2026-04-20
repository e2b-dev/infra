-- name: GetUser :one
SELECT id, email FROM "public"."users" where id = @user_id;
