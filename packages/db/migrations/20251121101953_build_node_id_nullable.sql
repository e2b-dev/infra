-- +goose Up
-- +goose StatementBegin
ALTER TABLE "public"."env_builds" ALTER COLUMN cluster_node_id DROP NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE "public"."env_builds" ALTER COLUMN cluster_node_id SET NOT NULL;
-- +goose StatementEnd
