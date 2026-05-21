-- +goose Up
-- +goose StatementBegin
ALTER TABLE tiers
    ALTER COLUMN "disk_mb" SET DEFAULT '16384'::bigint;
UPDATE tiers SET "disk_mb" = 16384 WHERE id = 'base_v1' AND "disk_mb" = 512;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE tiers
    ALTER COLUMN "disk_mb" SET DEFAULT '512'::bigint;
UPDATE tiers SET "disk_mb" = 512 WHERE id = 'base_v1' AND "disk_mb" = 16384;
-- +goose StatementEnd
