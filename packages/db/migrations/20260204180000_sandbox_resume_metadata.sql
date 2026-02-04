-- +goose Up
-- +goose StatementBegin
alter table snapshots
    add column if not exists sandbox_resumes_on varchar(10) null;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
alter table snapshots
    drop column if exists sandbox_resumes_on;
-- +goose StatementEnd
