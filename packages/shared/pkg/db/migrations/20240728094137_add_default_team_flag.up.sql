BEGIN;

-- Modify "access_tokens" table
ALTER TABLE "public"."users_teams" ADD COLUMN "is_default" boolean NOT NULL DEFAULT false;
UPDATE "public"."users_teams" ut SET "is_default" = t."is_default" FROM "public"."teams" t WHERE ut."team_id" = t."id";

CREATE OR REPLACE FUNCTION public.post_user_signup()
    RETURNS TRIGGER
    LANGUAGE plpgsql
AS $post_user_signup$
DECLARE
    team_id                 uuid;
BEGIN
    RAISE NOTICE 'Creating default team for user %', NEW.id;
    INSERT INTO public.teams(name, is_default, tier, email) VALUES (NEW.email, true, 'base_v1', NEW.email) RETURNING id INTO team_id;
    INSERT INTO public.users_teams(user_id, team_id, is_default) VALUES (NEW.id, team_id, true);
    RAISE NOTICE 'Created default team for user % and team %', NEW.id, team_id;

    -- Generate a random 20 byte string and encode it as hex, so it's 40 characters
    INSERT INTO public.team_api_keys (team_id)
    VALUES (team_id);

    INSERT INTO public.access_tokens (user_id)
    VALUES (NEW.id);

    PERFORM public.extra_for_post_user_signup(NEW.id, team_id);

    RETURN NEW;
END
$post_user_signup$ SECURITY DEFINER SET search_path = public;

ALTER FUNCTION public.post_user_signup() OWNER TO trigger_user;

COMMIT; 