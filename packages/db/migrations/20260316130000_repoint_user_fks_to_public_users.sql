-- +goose Up
-- +goose StatementBegin

-- Drop the FK from public.users to auth.users (public.users is now the source of truth)
ALTER TABLE public.users DROP CONSTRAINT IF EXISTS users_id_fkey;

-- Re-point all foreign keys from auth.users to public.users

ALTER TABLE public.access_tokens
  DROP CONSTRAINT access_tokens_users_access_tokens,
  ADD CONSTRAINT access_tokens_users_access_tokens
    FOREIGN KEY (user_id) REFERENCES public.users(id) ON UPDATE NO ACTION ON DELETE CASCADE;

ALTER TABLE public.users_teams
  DROP CONSTRAINT users_teams_users_users,
  ADD CONSTRAINT users_teams_users_users
    FOREIGN KEY (user_id) REFERENCES public.users(id) ON UPDATE NO ACTION ON DELETE CASCADE;

ALTER TABLE public.team_api_keys
  DROP CONSTRAINT team_api_keys_users_created_api_keys,
  ADD CONSTRAINT team_api_keys_users_created_api_keys
    FOREIGN KEY (created_by) REFERENCES public.users(id) ON UPDATE NO ACTION ON DELETE SET NULL;

ALTER TABLE public.envs
  DROP CONSTRAINT envs_users_created_envs,
  ADD CONSTRAINT envs_users_created_envs
    FOREIGN KEY (created_by) REFERENCES public.users(id) ON UPDATE NO ACTION ON DELETE SET NULL;

ALTER TABLE public.users_teams
  DROP CONSTRAINT users_teams_added_by_user,
  ADD CONSTRAINT users_teams_added_by_user
    FOREIGN KEY (added_by) REFERENCES public.users(id) ON UPDATE NO ACTION ON DELETE SET NULL;

ALTER TABLE public.addons
  DROP CONSTRAINT addons_users_addons,
  ADD CONSTRAINT addons_users_addons
    FOREIGN KEY (added_by) REFERENCES public.users(id) ON UPDATE NO ACTION ON DELETE NO ACTION;

-- PostgreSQL fires AFTER triggers alphabetically by name.
-- The post_user_signup trigger (p) must run AFTER the sync trigger
-- copies the user to public.users, since users_teams now has a FK to public.users.
-- Rename the sync trigger so it sorts before post_user_signup.
DROP TRIGGER IF EXISTS sync_inserts_to_public_users ON auth.users;
CREATE TRIGGER a0_sync_inserts_to_public_users
    AFTER INSERT ON auth.users
    FOR EACH ROW EXECUTE FUNCTION public.sync_insert_auth_users_to_public_users_trigger();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Restore original sync trigger name
DROP TRIGGER IF EXISTS a0_sync_inserts_to_public_users ON auth.users;
CREATE TRIGGER sync_inserts_to_public_users
    AFTER INSERT ON auth.users
    FOR EACH ROW EXECUTE FUNCTION public.sync_insert_auth_users_to_public_users_trigger();

ALTER TABLE public.users
  ADD CONSTRAINT users_id_fkey FOREIGN KEY (id) REFERENCES auth.users(id) ON DELETE CASCADE;

ALTER TABLE public.access_tokens
  DROP CONSTRAINT access_tokens_users_access_tokens,
  ADD CONSTRAINT access_tokens_users_access_tokens
    FOREIGN KEY (user_id) REFERENCES auth.users(id) ON UPDATE NO ACTION ON DELETE CASCADE;

ALTER TABLE public.users_teams
  DROP CONSTRAINT users_teams_users_users,
  ADD CONSTRAINT users_teams_users_users
    FOREIGN KEY (user_id) REFERENCES auth.users(id) ON UPDATE NO ACTION ON DELETE CASCADE;

ALTER TABLE public.team_api_keys
  DROP CONSTRAINT team_api_keys_users_created_api_keys,
  ADD CONSTRAINT team_api_keys_users_created_api_keys
    FOREIGN KEY (created_by) REFERENCES auth.users(id) ON UPDATE NO ACTION ON DELETE SET NULL;

ALTER TABLE public.envs
  DROP CONSTRAINT envs_users_created_envs,
  ADD CONSTRAINT envs_users_created_envs
    FOREIGN KEY (created_by) REFERENCES auth.users(id) ON UPDATE NO ACTION ON DELETE SET NULL;

ALTER TABLE public.users_teams
  DROP CONSTRAINT users_teams_added_by_user,
  ADD CONSTRAINT users_teams_added_by_user
    FOREIGN KEY (added_by) REFERENCES auth.users(id) ON UPDATE NO ACTION ON DELETE SET NULL;

ALTER TABLE public.addons
  DROP CONSTRAINT addons_users_addons,
  ADD CONSTRAINT addons_users_addons
    FOREIGN KEY (added_by) REFERENCES auth.users(id) ON UPDATE NO ACTION ON DELETE NO ACTION;

-- +goose StatementEnd
