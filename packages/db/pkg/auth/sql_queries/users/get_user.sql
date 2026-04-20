-- name: GetUser :one
SELECT * FROM "public"."users" where id = @user_id;
