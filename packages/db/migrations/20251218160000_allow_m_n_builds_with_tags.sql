-- +goose Up
-- +goose StatementBegin
-- 0. Create helper function to safely cast text to UUID
CREATE OR REPLACE FUNCTION try_cast_uuid(p_value text) RETURNS uuid AS $$
BEGIN
    RETURN p_value::uuid;
EXCEPTION WHEN invalid_text_representation THEN
    RETURN NULL;
END;
$$ LANGUAGE plpgsql IMMUTABLE;
-- +goose StatementEnd

-- +goose StatementBegin
-- 1. Create the new env_build_assignments table
CREATE TABLE env_build_assignments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    env_id TEXT NOT NULL,
    build_id UUID NOT NULL,
    tag TEXT NOT NULL,
    source TEXT DEFAULT 'app' NOT NULL, 
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    
    CONSTRAINT fk_env_build_assignments_env 
        FOREIGN KEY (env_id) 
        REFERENCES envs(id) 
        ON DELETE CASCADE,
    
    CONSTRAINT fk_env_build_assignments_build 
        FOREIGN KEY (build_id) 
        REFERENCES env_builds(id) 
        ON DELETE CASCADE
);

-- PARTIAL UNIQUE INDEX: 
-- Enforces that for 'trigger' or 'migration', only ONE entry exists per env/build/tag.
-- Does NOT restrict 'app' entries, allowing multiple assignments from code.
CREATE UNIQUE INDEX uq_legacy_assignments ON env_build_assignments (env_id, build_id, tag)
WHERE source IN ('trigger', 'migration');

ALTER TABLE "public"."env_build_assignments" ENABLE ROW LEVEL SECURITY;

CREATE INDEX idx_env_build_assignments_env_tag_created 
    ON env_build_assignments (env_id, tag, created_at DESC);

CREATE INDEX idx_env_build_assignments_env_build 
    ON env_build_assignments (env_id, build_id);
-- +goose StatementEnd

-- +goose StatementBegin
-- 2. Validation Trigger: Prevent "Mixed" sources (Trigger vs App)
-- If code ('app') creates an entry, we want to ignore or remove the 'trigger' version.
CREATE OR REPLACE FUNCTION validate_assignment_source_takeover()
RETURNS TRIGGER AS $$
BEGIN
    -- If trigger tries to insert but an 'app' record already exists, skip it
    IF NEW.source = 'trigger' THEN
        IF EXISTS (
            SELECT 1 FROM env_build_assignments 
            WHERE env_id = NEW.env_id AND build_id = NEW.build_id AND tag = NEW.tag AND source = 'app'
        ) THEN
            RETURN NULL; -- Silently ignore
        END IF;
    
    -- If 'app' inserts, we clean up any legacy 'trigger' or 'migration' records for this identity
    ELSIF NEW.source = 'app' THEN
        DELETE FROM env_build_assignments 
        WHERE env_id = NEW.env_id AND build_id = NEW.build_id AND tag = NEW.tag 
        AND source IN ('trigger', 'migration');
    END IF;
    
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_validate_assignment_source
    BEFORE INSERT ON env_build_assignments
    FOR EACH ROW EXECUTE FUNCTION validate_assignment_source_takeover();
-- +goose StatementEnd

-- +goose StatementBegin
-- 3. Legacy Sync Trigger function
CREATE OR REPLACE FUNCTION sync_env_build_assignment()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.env_id IS NOT NULL THEN
        IF TG_OP = 'INSERT' OR (TG_OP = 'UPDATE' AND OLD.env_id != NEW.env_id) THEN
            -- Note: ON CONFLICT refers to the partial index uq_legacy_assignments
            INSERT INTO env_build_assignments (env_id, build_id, tag, source, created_at)
            VALUES (NEW.env_id, NEW.id, 'default', 'trigger', CURRENT_TIMESTAMP)
            ON CONFLICT (env_id, build_id, tag) WHERE source IN ('trigger', 'migration') DO NOTHING;
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
-- 4. Create trigger to automatically sync changes
CREATE TRIGGER trigger_sync_env_build_assignment
    AFTER INSERT OR UPDATE ON env_builds
    FOR EACH ROW
    EXECUTE FUNCTION sync_env_build_assignment();
-- +goose StatementEnd

-- +goose StatementBegin
-- 5. Migrate existing data
INSERT INTO env_build_assignments (env_id, build_id, tag, source, created_at)
SELECT 
    env_id,
    id as build_id,
    'default' as tag,
    'migration' as source,
    created_at
FROM env_builds
WHERE env_id IS NOT NULL
ON CONFLICT (env_id, build_id, tag) WHERE source IN ('trigger', 'migration') DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS trigger_sync_env_build_assignment ON env_builds;
DROP TRIGGER IF EXISTS trigger_validate_assignment_source ON env_build_assignments;
DROP FUNCTION IF EXISTS sync_env_build_assignment();
DROP FUNCTION IF EXISTS validate_assignment_source_takeover();
DROP TABLE IF EXISTS env_build_assignments;
DROP FUNCTION IF EXISTS try_cast_uuid(text);
-- +goose StatementEnd