-- +goose Up
-- +goose StatementBegin

-- Drop the FK from public.users to auth.users (public.users is now the source of truth)
ALTER TABLE public.users DROP CONSTRAINT IF EXISTS users_id_fkey;

-- Re-point all foreign keys from auth.users to public.users.
-- Use NOT VALID to avoid ACCESS EXCLUSIVE lock scanning all rows,
-- then VALIDATE separately with a lighter ShareUpdateExclusiveLock.

ALTER TABLE public.access_tokens
  DROP CONSTRAINT access_tokens_users_access_tokens,
  ADD CONSTRAINT access_tokens_users_access_tokens
    FOREIGN KEY (user_id) REFERENCES public.users(id) ON UPDATE NO ACTION ON DELETE CASCADE
    NOT VALID;
ALTER TABLE public.access_tokens VALIDATE CONSTRAINT access_tokens_users_access_tokens;

ALTER TABLE public.users_teams
  DROP CONSTRAINT users_teams_users_users,
  ADD CONSTRAINT users_teams_users_users
    FOREIGN KEY (user_id) REFERENCES public.users(id) ON UPDATE NO ACTION ON DELETE CASCADE
    NOT VALID;
ALTER TABLE public.users_teams VALIDATE CONSTRAINT users_teams_users_users;

ALTER TABLE public.team_api_keys
  DROP CONSTRAINT team_api_keys_users_created_api_keys,
  ADD CONSTRAINT team_api_keys_users_created_api_keys
    FOREIGN KEY (created_by) REFERENCES public.users(id) ON UPDATE NO ACTION ON DELETE SET NULL
    NOT VALID;
ALTER TABLE public.team_api_keys VALIDATE CONSTRAINT team_api_keys_users_created_api_keys;

ALTER TABLE public.envs
  DROP CONSTRAINT envs_users_created_envs,
  ADD CONSTRAINT envs_users_created_envs
    FOREIGN KEY (created_by) REFERENCES public.users(id) ON UPDATE NO ACTION ON DELETE SET NULL
    NOT VALID;
ALTER TABLE public.envs VALIDATE CONSTRAINT envs_users_created_envs;

ALTER TABLE public.users_teams
  DROP CONSTRAINT users_teams_added_by_user,
  ADD CONSTRAINT users_teams_added_by_user
    FOREIGN KEY (added_by) REFERENCES public.users(id) ON UPDATE NO ACTION ON DELETE SET NULL
    NOT VALID;
ALTER TABLE public.users_teams VALIDATE CONSTRAINT users_teams_added_by_user;

ALTER TABLE public.addons
  DROP CONSTRAINT addons_users_addons,
  ADD CONSTRAINT addons_users_addons
    FOREIGN KEY (added_by) REFERENCES public.users(id) ON UPDATE NO ACTION ON DELETE NO ACTION
    NOT VALID;
ALTER TABLE public.addons VALIDATE CONSTRAINT addons_users_addons;

-- PostgreSQL fires AFTER triggers alphabetically by name.
-- The post_user_signup trigger (p) must run AFTER the sync trigger
-- copies the user to public.users, since users_teams now has a FK to public.users.
-- Rename the sync trigger so it sorts before post_user_signup.
DROP TRIGGER IF EXISTS sync_inserts_to_public_users ON auth.users;
CREATE TRIGGER a0_sync_inserts_to_public_users
    AFTER INSERT ON auth.users
    FOR EACH ROW EXECUTE FUNCTION public.sync_insert_auth_users_to_public_users_trigger();

-- Dropping users_id_fkey removed the ON DELETE CASCADE from auth.users to public.users.
-- Add a delete-sync trigger so auth.users deletions still propagate to public.users
-- (which then cascades to access_tokens, users_teams, etc. via the re-pointed FKs).
GRANT DELETE ON public.users TO trigger_user;
CREATE POLICY "Allow to delete a user"
    ON public.users
    AS PERMISSIVE
    FOR DELETE
    TO trigger_user
    USING (true);

CREATE OR REPLACE FUNCTION public.sync_delete_auth_users_to_public_users_trigger() RETURNS TRIGGER
LANGUAGE plpgsql
AS $func$
BEGIN
    DELETE FROM public.users WHERE id = OLD.id;
    RETURN OLD;
END;
$func$ SECURITY DEFINER SET search_path = public;

ALTER FUNCTION public.sync_delete_auth_users_to_public_users_trigger() OWNER TO trigger_user;

CREATE TRIGGER a0_sync_deletes_to_public_users
    AFTER DELETE ON auth.users
    FOR EACH ROW EXECUTE FUNCTION public.sync_delete_auth_users_to_public_users_trigger();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Drop delete-sync trigger and function
DROP TRIGGER IF EXISTS a0_sync_deletes_to_public_users ON auth.users;
DROP FUNCTION IF EXISTS public.sync_delete_auth_users_to_public_users_trigger();
DROP POLICY IF EXISTS "Allow to delete a user" ON public.users;
REVOKE DELETE ON public.users FROM trigger_user;

-- Restore original insert-sync trigger name
DROP TRIGGER IF EXISTS a0_sync_inserts_to_public_users ON auth.users;
CREATE TRIGGER sync_inserts_to_public_users
    AFTER INSERT ON auth.users
    FOR EACH ROW EXECUTE FUNCTION public.sync_insert_auth_users_to_public_users_trigger();

-- NOTE: This down migration is only safe within a narrow window after deploy.
-- Once new users sign up (inserted into public.users via triggers), rolling back
-- will fail if those users don't have corresponding auth.users rows.
ALTER TABLE public.users
  ADD CONSTRAINT users_id_fkey FOREIGN KEY (id) REFERENCES auth.users(id) ON DELETE CASCADE
  NOT VALID;
ALTER TABLE public.users VALIDATE CONSTRAINT users_id_fkey;

ALTER TABLE public.access_tokens
  DROP CONSTRAINT access_tokens_users_access_tokens,
  ADD CONSTRAINT access_tokens_users_access_tokens
    FOREIGN KEY (user_id) REFERENCES auth.users(id) ON UPDATE NO ACTION ON DELETE CASCADE
    NOT VALID;
ALTER TABLE public.access_tokens VALIDATE CONSTRAINT access_tokens_users_access_tokens;

ALTER TABLE public.users_teams
  DROP CONSTRAINT users_teams_users_users,
  ADD CONSTRAINT users_teams_users_users
    FOREIGN KEY (user_id) REFERENCES auth.users(id) ON UPDATE NO ACTION ON DELETE CASCADE
    NOT VALID;
ALTER TABLE public.users_teams VALIDATE CONSTRAINT users_teams_users_users;

ALTER TABLE public.team_api_keys
  DROP CONSTRAINT team_api_keys_users_created_api_keys,
  ADD CONSTRAINT team_api_keys_users_created_api_keys
    FOREIGN KEY (created_by) REFERENCES auth.users(id) ON UPDATE NO ACTION ON DELETE SET NULL
    NOT VALID;
ALTER TABLE public.team_api_keys VALIDATE CONSTRAINT team_api_keys_users_created_api_keys;

ALTER TABLE public.envs
  DROP CONSTRAINT envs_users_created_envs,
  ADD CONSTRAINT envs_users_created_envs
    FOREIGN KEY (created_by) REFERENCES auth.users(id) ON UPDATE NO ACTION ON DELETE SET NULL
    NOT VALID;
ALTER TABLE public.envs VALIDATE CONSTRAINT envs_users_created_envs;

ALTER TABLE public.users_teams
  DROP CONSTRAINT users_teams_added_by_user,
  ADD CONSTRAINT users_teams_added_by_user
    FOREIGN KEY (added_by) REFERENCES auth.users(id) ON UPDATE NO ACTION ON DELETE SET NULL
    NOT VALID;
ALTER TABLE public.users_teams VALIDATE CONSTRAINT users_teams_added_by_user;

ALTER TABLE public.addons
  DROP CONSTRAINT addons_users_addons,
  ADD CONSTRAINT addons_users_addons
    FOREIGN KEY (added_by) REFERENCES auth.users(id) ON UPDATE NO ACTION ON DELETE NO ACTION
    NOT VALID;
ALTER TABLE public.addons VALIDATE CONSTRAINT addons_users_addons;

-- +goose StatementEnd
