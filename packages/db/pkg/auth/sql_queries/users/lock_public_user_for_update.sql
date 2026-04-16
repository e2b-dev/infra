-- name: LockPublicUserForUpdate :one
SELECT id
FROM public.users
WHERE id = @id
FOR UPDATE;
