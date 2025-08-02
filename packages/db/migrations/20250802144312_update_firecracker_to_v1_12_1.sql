-- +goose Up
-- +goose StatementBegin

-- Update firecracker version default in env_builds table
ALTER TABLE "public"."env_builds"
ALTER COLUMN "firecracker_version" SET DEFAULT 'v1.12.1_d990331';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Rollback firecracker version default in env_builds table
ALTER TABLE "public"."env_builds"
ALTER COLUMN "firecracker_version" SET DEFAULT 'v1.10.1_1fcdaec';

-- +goose StatementEnd