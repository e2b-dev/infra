-- +goose Up
-- +goose StatementBegin

CREATE SCHEMA IF NOT EXISTS auth_custom;

CREATE OR REPLACE FUNCTION public.sync_insert_auth_users_to_public_users_trigger() RETURNS TRIGGER
LANGUAGE plpgsql
AS $func$
BEGIN
    INSERT INTO auth_custom.river_job (args, kind, max_attempts, queue, state)
    VALUES (
        jsonb_build_object('user_id', NEW.id, 'operation', 'upsert', 'email', NEW.email),
        'auth_user_sync',
        20,
        'auth_sync',
        'available'
    );

    PERFORM pg_notify('auth_custom.river_insert', '{"queue":"auth_sync"}');

    RETURN NEW;
END;
$func$ SECURITY DEFINER SET search_path = public, auth_custom;

CREATE OR REPLACE FUNCTION public.sync_update_auth_users_to_public_users_trigger() RETURNS TRIGGER
LANGUAGE plpgsql
AS $func$
BEGIN
    IF OLD.email IS DISTINCT FROM NEW.email THEN
        INSERT INTO auth_custom.river_job (args, kind, max_attempts, queue, state)
        VALUES (
            jsonb_build_object('user_id', NEW.id, 'operation', 'upsert', 'email', NEW.email),
            'auth_user_sync',
            20,
            'auth_sync',
            'available'
        );

        PERFORM pg_notify('auth_custom.river_insert', '{"queue":"auth_sync"}');
    END IF;

    RETURN NEW;
END;
$func$ SECURITY DEFINER SET search_path = public, auth_custom;

CREATE OR REPLACE FUNCTION public.sync_delete_auth_users_to_public_users_trigger() RETURNS TRIGGER
LANGUAGE plpgsql
AS $func$
BEGIN
    INSERT INTO auth_custom.river_job (args, kind, max_attempts, queue, state)
    VALUES (
        jsonb_build_object('user_id', OLD.id, 'operation', 'delete'),
        'auth_user_sync',
        20,
        'auth_sync',
        'available'
    );

    PERFORM pg_notify('auth_custom.river_insert', '{"queue":"auth_sync"}');

    RETURN OLD;
END;
$func$ SECURITY DEFINER SET search_path = public, auth_custom;

ALTER FUNCTION public.sync_insert_auth_users_to_public_users_trigger() OWNER TO trigger_user;
ALTER FUNCTION public.sync_update_auth_users_to_public_users_trigger() OWNER TO trigger_user;
ALTER FUNCTION public.sync_delete_auth_users_to_public_users_trigger() OWNER TO trigger_user;

DROP TRIGGER IF EXISTS sync_inserts_to_public_users ON auth.users;
CREATE TRIGGER sync_inserts_to_public_users
    AFTER INSERT ON auth.users
    FOR EACH ROW EXECUTE FUNCTION public.sync_insert_auth_users_to_public_users_trigger();

DROP TRIGGER IF EXISTS sync_updates_to_public_users ON auth.users;
CREATE TRIGGER sync_updates_to_public_users
    AFTER UPDATE ON auth.users
    FOR EACH ROW EXECUTE FUNCTION public.sync_update_auth_users_to_public_users_trigger();

DROP TRIGGER IF EXISTS sync_deletes_to_public_users ON auth.users;
CREATE TRIGGER sync_deletes_to_public_users
    AFTER DELETE ON auth.users
    FOR EACH ROW EXECUTE FUNCTION public.sync_delete_auth_users_to_public_users_trigger();

GRANT USAGE ON SCHEMA auth_custom TO trigger_user;
GRANT INSERT ON auth_custom.river_job TO trigger_user;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA auth_custom TO trigger_user;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS sync_inserts_to_public_users ON auth.users;
DROP TRIGGER IF EXISTS sync_updates_to_public_users ON auth.users;
DROP TRIGGER IF EXISTS sync_deletes_to_public_users ON auth.users;

CREATE OR REPLACE FUNCTION public.sync_insert_auth_users_to_public_users_trigger() RETURNS TRIGGER
LANGUAGE plpgsql
AS $func$
BEGIN
    INSERT INTO public.user_sync_queue (user_id, operation)
    VALUES (NEW.id, 'upsert');

    RETURN NEW;
END;
$func$ SECURITY DEFINER SET search_path = public;

CREATE OR REPLACE FUNCTION public.sync_update_auth_users_to_public_users_trigger() RETURNS TRIGGER
LANGUAGE plpgsql
AS $func$
BEGIN
    IF OLD.email IS DISTINCT FROM NEW.email THEN
        INSERT INTO public.user_sync_queue (user_id, operation)
        VALUES (NEW.id, 'upsert');
    END IF;

    RETURN NEW;
END;
$func$ SECURITY DEFINER SET search_path = public;

CREATE OR REPLACE FUNCTION public.sync_delete_auth_users_to_public_users_trigger() RETURNS TRIGGER
LANGUAGE plpgsql
AS $func$
BEGIN
    INSERT INTO public.user_sync_queue (user_id, operation)
    VALUES (OLD.id, 'delete');

    RETURN OLD;
END;
$func$ SECURITY DEFINER SET search_path = public;

ALTER FUNCTION public.sync_insert_auth_users_to_public_users_trigger() OWNER TO trigger_user;
ALTER FUNCTION public.sync_update_auth_users_to_public_users_trigger() OWNER TO trigger_user;
ALTER FUNCTION public.sync_delete_auth_users_to_public_users_trigger() OWNER TO trigger_user;

CREATE TRIGGER sync_inserts_to_public_users
    AFTER INSERT ON auth.users
    FOR EACH ROW EXECUTE FUNCTION public.sync_insert_auth_users_to_public_users_trigger();

CREATE TRIGGER sync_updates_to_public_users
    AFTER UPDATE ON auth.users
    FOR EACH ROW EXECUTE FUNCTION public.sync_update_auth_users_to_public_users_trigger();

CREATE TRIGGER sync_deletes_to_public_users
    AFTER DELETE ON auth.users
    FOR EACH ROW EXECUTE FUNCTION public.sync_delete_auth_users_to_public_users_trigger();

REVOKE ALL ON SCHEMA auth_custom FROM trigger_user;

DROP SCHEMA IF EXISTS auth_custom CASCADE;

-- +goose StatementEnd
