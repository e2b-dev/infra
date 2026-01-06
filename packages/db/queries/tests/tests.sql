-- name: Test_CreateTeam :exec
INSERT INTO teams (id, email, name, tier, is_blocked) VALUES (@id, @email, @name, @tier, @blocked);

-- name: Test_CreateUser :exec
INSERT INTO auth.users (id, email) VALUES (@id, @email);

-- name: Test_AddUserToTeams :exec
INSERT INTO users_teams (user_id, team_id, is_default) VALUES (@user_id, @team_id, @is_default);