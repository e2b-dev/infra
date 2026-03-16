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

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

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
