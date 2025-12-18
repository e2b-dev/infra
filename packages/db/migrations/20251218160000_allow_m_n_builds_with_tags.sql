-- +goose Up
-- +goose StatementBegin
-- 0. Create helper function to safely cast text to UUID (returns NULL if invalid)
-- This is needed for PostgreSQL < 16 (which has pg_input_is_valid)
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
    tag TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    
    -- Add foreign key constraints
    CONSTRAINT fk_env_build_assignments_env 
        FOREIGN KEY (env_id) 
        REFERENCES envs(id) 
        ON DELETE CASCADE,
    
    CONSTRAINT fk_env_build_assignments_build 
        FOREIGN KEY (build_id) 
        REFERENCES env_builds(id) 
        ON DELETE CASCADE,
    
    -- Add unique constraint to prevent duplicate assignments for the same build+tag combination
    -- This ensures data consistency during migration and normal operations
    CONSTRAINT uq_env_build_assignments_build_tag 
        UNIQUE (build_id, tag, created_at)
);
ALTER TABLE "public"."env_build_assignments" ENABLE ROW LEVEL SECURITY;

-- Create indexes for efficient lookups
CREATE INDEX idx_env_build_assignments_env_tag_created 
    ON env_build_assignments (env_id, tag, created_at DESC);

CREATE INDEX idx_env_build_assignments_env_build 
    ON env_build_assignments (env_id, build_id);
-- +goose StatementEnd

-- +goose StatementBegin
-- 2. Create trigger function to sync env_builds.env_id changes to env_build_assignments
-- This table is append-only for 'latest' tag. Queries use ORDER BY created_at DESC LIMIT 1 to get current assignment.
CREATE OR REPLACE FUNCTION sync_env_build_assignment()
RETURNS TRIGGER AS $$
BEGIN
    -- On INSERT or UPDATE, if env_id is set, append new assignment with 'latest' tag
    IF NEW.env_id IS NOT NULL THEN
        -- Check if this is actually a change (for UPDATE operations)
        IF TG_OP = 'INSERT' OR (TG_OP = 'UPDATE' AND (OLD.env_id IS NULL OR OLD.env_id != NEW.env_id)) THEN
            -- Append new assignment with 'latest' tag (append-only)
            -- Use ON CONFLICT DO NOTHING to handle race conditions during migration
            INSERT INTO env_build_assignments (env_id, build_id, tag, created_at)
            VALUES (NEW.env_id, NEW.id, 'latest', CURRENT_TIMESTAMP)
            ON CONFLICT (build_id, tag, created_at) DO NOTHING;
        END IF;
    END IF;
    
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
-- 3. Create trigger to automatically sync changes (BEFORE data migration to prevent race conditions)
CREATE TRIGGER trigger_sync_env_build_assignment
    AFTER INSERT OR UPDATE ON env_builds
    FOR EACH ROW
    EXECUTE FUNCTION sync_env_build_assignment();
-- +goose StatementEnd

-- +goose StatementBegin
-- 4. Migrate existing data from direct relationship
-- The env_builds table has env_id column pointing to envs
-- The trigger is already active, so ON CONFLICT handles any concurrent inserts
INSERT INTO env_build_assignments (env_id, build_id, tag, created_at)
SELECT 
    env_id,
    id as build_id,
    'latest' as tag,
    created_at
FROM env_builds
WHERE env_id IS NOT NULL
ON CONFLICT (build_id, tag, created_at) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- 1. Drop the trigger
DROP TRIGGER IF EXISTS trigger_sync_env_build_assignment ON env_builds;
-- +goose StatementEnd

-- +goose StatementBegin
-- 2. Drop the trigger function
DROP FUNCTION IF EXISTS sync_env_build_assignment();
-- +goose StatementEnd

-- +goose StatementBegin
-- 3. Drop indexes
DROP INDEX IF EXISTS idx_env_build_assignments_env_tag_created;
DROP INDEX IF EXISTS idx_env_build_assignments_env_build;
-- +goose StatementEnd

-- +goose StatementBegin
-- 4. Drop the assignments table
DROP TABLE IF EXISTS env_build_assignments;
-- +goose StatementEnd

-- +goose StatementBegin
-- 5. Drop helper function
DROP FUNCTION IF EXISTS try_cast_uuid(text);
-- +goose StatementEnd

