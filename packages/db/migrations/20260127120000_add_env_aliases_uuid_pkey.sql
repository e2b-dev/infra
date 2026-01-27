-- +goose Up
-- +goose NO TRANSACTION

-- 1. Add the column (allowing NULLs temporarily)
ALTER TABLE public.env_aliases 
  ADD COLUMN id UUID;

-- 2. Populate the column with unique IDs
UPDATE public.env_aliases 
  SET id = gen_random_uuid();

-- 3. Make the column NOT NULL and set as Primary Key
ALTER TABLE public.env_aliases 
  ALTER COLUMN id SET NOT NULL,
  ALTER COLUMN id SET DEFAULT gen_random_uuid(),
  DROP CONSTRAINT env_aliases_pkey,
  ADD CONSTRAINT env_aliases_uuid_pkey PRIMARY KEY (id);

-- 4. Drop the existing non-unique index
DROP INDEX CONCURRENTLY IF EXISTS idx_env_aliases_alias_namespace;

-- 5. Create unique index for the alias/namespace combo
-- Using 'NULLS NOT DISTINCT' so (alias, NULL) is treated as a unique pair
CREATE UNIQUE INDEX CONCURRENTLY idx_env_aliases_alias_namespace_unique 
  ON public.env_aliases (alias, namespace) 
  NULLS NOT DISTINCT;

-- +goose Down
-- +goose NO TRANSACTION

DROP INDEX CONCURRENTLY IF EXISTS idx_env_aliases_alias_namespace_unique;

-- Recreate the original non-unique index
CREATE INDEX CONCURRENTLY idx_env_aliases_alias_namespace 
  ON public.env_aliases (alias, namespace);

ALTER TABLE public.env_aliases 
  DROP CONSTRAINT env_aliases_uuid_pkey,
  ADD CONSTRAINT env_aliases_pkey PRIMARY KEY (alias),
  DROP COLUMN id;
