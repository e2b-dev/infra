-- +goose Up
-- +goose StatementBegin
ALTER TABLE tiers
    ALTER COLUMN "disk_mb" SET DEFAULT '8192'::bigint;
UPDATE tiers SET "disk_mb" = 8192 WHERE id = 'base_v1' AND "disk_mb" = 512;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE tiers
    ALTER COLUMN "disk_mb" SET DEFAULT '512'::bigint;
UPDATE tiers SET "disk_mb" = 512 WHERE id = 'base_v1' AND "disk_mb" = 8192;
-- +goose StatementEnd
