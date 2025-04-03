PRAGMA integrity_check;
PRAGMA foreign_keys = ON;

CREATE TABLE sandboxes (
  id              TEXT        PRIMARY KEY  NOT NULL,
  started_at      TIMESTAMP   NOT NULL     DEFAULT current_timestamp,
  updated_at      TIMESTAMP   NOT NULL     DEFAULT current_timestamp,
  deadline        TIMESTAMP   NOT NULL,
  status          TEXT        CHECK( status IN ('pending', 'running', 'paused', 'terminated'))
                              NOT NULL     DEFAULT 'pending',
  duration_ms     INTEGER     CHECK( duration_ms >= 0 )
                              NOT NULL     DEFAULT 0,
  version         INTEGER     CHECK( version >= 0 )
                              NOT NULL     DEFAULT 0,
  global_version  INTEGER     CHECK( global_version >= 0 )
                              NOT NULL,
  config          BLOB
);

CREATE TABLE status (
  id              INTEGER     PRIMARY KEY  NOT NULL,
  version         INTEGER     NOT NULL     DEFAULT 0,
  updated_at      TIMESTAMP   NOT NULL     DEFAULT current_timestamp,
  status          TEXT        CHECK( status IN ('initializing', 'running', 'draining', 'quarantined ', 'terminated'))
                              NOT NULL     DEFAULT 'initializing'
);

INSERT INTO status(id) VALUES(1);
