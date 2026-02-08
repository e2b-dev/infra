-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS volumes (
    id          UUID                        PRIMARY KEY     DEFAULT gen_random_uuid(),
    team_id     UUID                        NOT NULL,
    name        VARCHAR(250)                NOT NULL,
    volume_type VARCHAR(250)                NOT NULL,
    created_at  TIMESTAMP WITH TIME ZONE    NOT NULL        DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT fk_volumes_teams
        FOREIGN KEY (team_id)
        REFERENCES teams(id),

    CONSTRAINT volumes_teams_uq
        UNIQUE (team_id, name)
);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE "public"."volumes" ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE POLICY "Allow selection for users that are in the team"
    ON "public"."volumes"
    AS PERMISSIVE
    FOR SELECT
    TO authenticated
    USING ((auth.uid() IN ( SELECT users_teams.user_id
                            FROM users_teams
                            WHERE (users_teams.team_id = volumes.team_id))));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS volumes;
-- +goose StatementEnd
