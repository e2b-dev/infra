-- +goose Up
-- +goose StatementBegin
alter table snapshots
    add column sandbox_resumes_on varchar(10) null;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
alter table snapshots
    drop column sandbox_resumes_on;
-- +goose StatementEnd
