-- This file provides schema definitions for sqlc that can't be parsed from migrations.
-- Migrations wrapped in DO $$ blocks are not visible to sqlc's static parser.
-- These statements are not executed - they're only read by sqlc for code generation.

ALTER TABLE teams ADD COLUMN slug TEXT NOT NULL;
