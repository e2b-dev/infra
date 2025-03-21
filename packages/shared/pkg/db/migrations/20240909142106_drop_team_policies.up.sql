BEGIN;

DROP POLICY "Allow update for users that are in the team" ON "public"."teams";
DROP POLICY "Allow users to delete a team user entry" ON "public"."users_teams";
DROP POLICY "Allow users to create a new team user entry" ON "public"."users_teams";

COMMIT; 