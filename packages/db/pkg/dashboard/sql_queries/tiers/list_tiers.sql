-- name: ListTiers :many
SELECT id, name
FROM public.tiers
ORDER BY name;
