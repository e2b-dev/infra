-- name: CreateSnapshotTemplateBuild :one
-- Creates a standalone env_builds row for a snapshot-template build that is
-- derived from (and shares data with) another build of the same checkpoint,
-- e.g. the filesystem-only sibling of a memory checkpoint. CPU info is copied
-- from the source build so the derived build keeps the snapshot's CPU
-- compatibility pinned to the original build, mirroring UpsertSnapshot.
INSERT INTO "public"."env_builds" (
    vcpu,
    ram_mb,
    free_disk_size_mb,
    kernel_version,
    firecracker_version,
    envd_version,
    status,
    cluster_node_id,
    total_disk_size_mb,
    updated_at,
    cpu_architecture,
    cpu_family,
    cpu_model,
    cpu_model_name,
    cpu_flags
)
VALUES (
    @vcpu,
    @ram_mb,
    @free_disk_size_mb,
    @kernel_version,
    @firecracker_version,
    @envd_version,
    @status,
    @origin_node_id,
    @total_disk_size_mb,
    now(),
    (SELECT eb.cpu_architecture FROM "public"."env_builds" eb WHERE eb.id = @source_build_id),
    (SELECT eb.cpu_family FROM "public"."env_builds" eb WHERE eb.id = @source_build_id),
    (SELECT eb.cpu_model FROM "public"."env_builds" eb WHERE eb.id = @source_build_id),
    (SELECT eb.cpu_model_name FROM "public"."env_builds" eb WHERE eb.id = @source_build_id),
    (SELECT eb.cpu_flags FROM "public"."env_builds" eb WHERE eb.id = @source_build_id)
)
RETURNING id as build_id;
