-- name: UpdateSnapshotSchedulingMetadata :exec
UPDATE "public"."snapshots"
SET metadata = jsonb_set(
    metadata,
    '{snapshot_scheduling_metadata}',
    (@metadata::text)::jsonb,
    true
)
WHERE sandbox_id = @sandbox_id;
