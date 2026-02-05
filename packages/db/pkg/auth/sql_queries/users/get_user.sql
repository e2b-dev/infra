-- name: GetUser :one
SELECT * FROM "auth"."users" where id = @user_id;
