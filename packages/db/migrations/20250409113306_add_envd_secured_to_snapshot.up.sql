BEGIN;

ALTER TABLE snapshots
ADD COLUMN env_secure boolean NOT NULL DEFAULT false;

COMMIT;
