-- Project reconciliation for the control-plane management interface. The caller
-- supplies the id, so create and reconcile are one request and these are its
-- branches.

-- Taken first, so the branch is decided by whether the project exists rather
-- than by an insert failing.
-- name: LockManagedTeam :one
SELECT id, slug FROM public.teams
WHERE id = sqlc.arg(id)::uuid
FOR UPDATE;

-- Tier is assigned here and only here. DO NOTHING covers the case where the
-- lock above found nothing and another request inserted the same id before
-- this ran: no row means that happened. A slug collision is a different
-- constraint and still raises.
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

-- Touches only the properties the caller synchronizes. Tier stays because it is
-- this side's to assign; is_blocked stays because an operator's decision must
-- outlive a routine push.
-- name: UpdateManagedTeam :one
UPDATE public.teams
SET
    name = sqlc.arg(name)::text,
    email = sqlc.arg(email)::text
WHERE id = sqlc.arg(id)::uuid
RETURNING id, name, slug, email;
