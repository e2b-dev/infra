-- +goose Up
CREATE TABLE counts (
    key     TEXT        NOT NULL    PRIMARY KEY,
    count   INTEGER     NOT NULL    DEFAULT(0),
    setID   TEXT        NOT NULL
);

-- +goose Down
DROP TABLE counts;
