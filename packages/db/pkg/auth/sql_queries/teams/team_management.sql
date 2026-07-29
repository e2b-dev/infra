-- Project reconciliation for the control-plane management interface. The caller
-- supplies the id, so create and reconcile are one request and these are its
-- branches.

-- Yields no row when the id is taken, which is the signal to reconcile. DO
-- NOTHING rather than DO UPDATE, so a slug collision still surfaces as the
-- distinct violation it is.
-- name: InsertManagedTeam :one
INSERT INTO public.teams (id, name, slug, tier, email, is_blocked)
VALUES (
    sqlc.arg(id)::uuid,
    sqlc.arg(name)::text,
    sqlc.arg(slug)::text,
    sqlc.arg(tier)::text,
    sqlc.arg(email)::text,
    false
)
ON CONFLICT (id) DO NOTHING
RETURNING id, name, slug, email;

-- name: LockManagedTeam :one
SELECT id, slug FROM public.teams
WHERE id = sqlc.arg(id)::uuid
FOR UPDATE;

-- Touches only what a reconcile may change. Tier stays because limits arrive
-- through their own route; is_blocked stays because an operator's decision must
-- outlive a routine name push.
-- name: UpdateManagedTeam :one
UPDATE public.teams
SET
    name = sqlc.arg(name)::text,
    email = COALESCE(sqlc.narg(email)::text, email)
WHERE id = sqlc.arg(id)::uuid
RETURNING id, name, slug, email;
