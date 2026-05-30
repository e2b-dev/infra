-- Manual cleanup for existing non-Supabase Postgres databases.
-- Do not run this on Supabase projects; new Supabase tables should use Supabase's RLS automation.
-- Assumes the database has already applied the latest migrations.

BEGIN;

DROP POLICY IF EXISTS "Allow selection for users that are in the team" ON public.teams;

DROP POLICY IF EXISTS "Enable select for users in relevant team" ON public.users_teams;

DROP POLICY IF EXISTS "Enable select for users based on user_id" ON public.access_tokens;

DROP POLICY IF EXISTS "Allow selection for users that are in the team" ON public.team_api_keys;
DROP POLICY IF EXISTS "Allow users to delete a team api key" ON public.team_api_keys;

DROP FUNCTION IF EXISTS public.is_member_of_team(uuid, uuid);

ALTER TABLE IF EXISTS public._migrations NO FORCE ROW LEVEL SECURITY;
ALTER TABLE IF EXISTS public._migrations DISABLE ROW LEVEL SECURITY;

ALTER TABLE IF EXISTS public.tiers NO FORCE ROW LEVEL SECURITY;
ALTER TABLE IF EXISTS public.tiers DISABLE ROW LEVEL SECURITY;

ALTER TABLE IF EXISTS public.teams NO FORCE ROW LEVEL SECURITY;
ALTER TABLE IF EXISTS public.teams DISABLE ROW LEVEL SECURITY;

ALTER TABLE IF EXISTS public.envs NO FORCE ROW LEVEL SECURITY;
ALTER TABLE IF EXISTS public.envs DISABLE ROW LEVEL SECURITY;

ALTER TABLE IF EXISTS public.env_aliases NO FORCE ROW LEVEL SECURITY;
ALTER TABLE IF EXISTS public.env_aliases DISABLE ROW LEVEL SECURITY;

ALTER TABLE IF EXISTS public.team_api_keys NO FORCE ROW LEVEL SECURITY;
ALTER TABLE IF EXISTS public.team_api_keys DISABLE ROW LEVEL SECURITY;

ALTER TABLE IF EXISTS public.access_tokens NO FORCE ROW LEVEL SECURITY;
ALTER TABLE IF EXISTS public.access_tokens DISABLE ROW LEVEL SECURITY;

ALTER TABLE IF EXISTS public.users_teams NO FORCE ROW LEVEL SECURITY;
ALTER TABLE IF EXISTS public.users_teams DISABLE ROW LEVEL SECURITY;

ALTER TABLE IF EXISTS public.env_builds NO FORCE ROW LEVEL SECURITY;
ALTER TABLE IF EXISTS public.env_builds DISABLE ROW LEVEL SECURITY;

ALTER TABLE IF EXISTS public.snapshots NO FORCE ROW LEVEL SECURITY;
ALTER TABLE IF EXISTS public.snapshots DISABLE ROW LEVEL SECURITY;

ALTER TABLE IF EXISTS public.clusters NO FORCE ROW LEVEL SECURITY;
ALTER TABLE IF EXISTS public.clusters DISABLE ROW LEVEL SECURITY;

ALTER TABLE IF EXISTS public.addons NO FORCE ROW LEVEL SECURITY;
ALTER TABLE IF EXISTS public.addons DISABLE ROW LEVEL SECURITY;

ALTER TABLE IF EXISTS public.env_build_assignments NO FORCE ROW LEVEL SECURITY;
ALTER TABLE IF EXISTS public.env_build_assignments DISABLE ROW LEVEL SECURITY;

ALTER TABLE IF EXISTS public.users NO FORCE ROW LEVEL SECURITY;
ALTER TABLE IF EXISTS public.users DISABLE ROW LEVEL SECURITY;

ALTER TABLE IF EXISTS public.snapshot_templates NO FORCE ROW LEVEL SECURITY;
ALTER TABLE IF EXISTS public.snapshot_templates DISABLE ROW LEVEL SECURITY;

ALTER TABLE IF EXISTS public.volumes NO FORCE ROW LEVEL SECURITY;
ALTER TABLE IF EXISTS public.volumes DISABLE ROW LEVEL SECURITY;

ALTER TABLE IF EXISTS public.active_template_builds NO FORCE ROW LEVEL SECURITY;
ALTER TABLE IF EXISTS public.active_template_builds DISABLE ROW LEVEL SECURITY;

ALTER TABLE IF EXISTS public.user_identities NO FORCE ROW LEVEL SECURITY;
ALTER TABLE IF EXISTS public.user_identities DISABLE ROW LEVEL SECURITY;

ALTER TABLE IF EXISTS public.env_defaults NO FORCE ROW LEVEL SECURITY;
ALTER TABLE IF EXISTS public.env_defaults DISABLE ROW LEVEL SECURITY;

COMMIT;
