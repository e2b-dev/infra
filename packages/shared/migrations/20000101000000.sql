CREATE SCHEMA auth;
CREATE ROLE authenticated;
CREATE FUNCTION auth.uid() RETURNS uuid AS $$
BEGIN
END;
$$ LANGUAGE plpgsql;

-- Create "users" table
CREATE TABLE "auth"."users"
(
    "id"                   uuid                     NOT NULL DEFAULT gen_random_uuid(),
    "email"                character varying(255)   NOT NULL,
    PRIMARY KEY ("id")
);
