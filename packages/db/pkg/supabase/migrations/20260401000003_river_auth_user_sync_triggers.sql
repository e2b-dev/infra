-- +goose Up
-- +goose StatementBegin

CREATE OR REPLACE FUNCTION auth_custom.enqueue_user_sync_on_insert()
RETURNS TRIGGER
LANGUAGE plpgsql
SECURITY DEFINER SET search_path = ''
AS $$
BEGIN
    INSERT INTO auth_custom.river_job (args, kind, max_attempts, queue, state)
    VALUES (
        jsonb_build_object('user_id', NEW.id, 'operation', 'upsert'),
        'auth_user_projection',
        20,
        'auth_user_projection',
        'available'
    );

    PERFORM pg_notify('auth_custom.river_insert', '{"queue":"auth_user_projection"}');

    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION auth_custom.enqueue_user_sync_on_update()
RETURNS TRIGGER
LANGUAGE plpgsql
SECURITY DEFINER SET search_path = ''
AS $$
BEGIN
    IF OLD.email IS DISTINCT FROM NEW.email THEN
        INSERT INTO auth_custom.river_job (args, kind, max_attempts, queue, state)
        VALUES (
            jsonb_build_object('user_id', NEW.id, 'operation', 'upsert'),
            'auth_user_projection',
            20,
            'auth_user_projection',
            'available'
        );

        PERFORM pg_notify('auth_custom.river_insert', '{"queue":"auth_user_projection"}');
    END IF;

    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION auth_custom.enqueue_user_sync_on_delete()
RETURNS TRIGGER
LANGUAGE plpgsql
SECURITY DEFINER SET search_path = ''
AS $$
BEGIN
    INSERT INTO auth_custom.river_job (args, kind, max_attempts, queue, state)
    VALUES (
        jsonb_build_object('user_id', OLD.id, 'operation', 'delete'),
        'auth_user_projection',
        20,
        'auth_user_projection',
        'available'
    );

    PERFORM pg_notify('auth_custom.river_insert', '{"queue":"auth_user_projection"}');

    RETURN OLD;
END;
$$;

CREATE TRIGGER enqueue_user_sync_on_insert
    AFTER INSERT ON auth.users
    FOR EACH ROW EXECUTE FUNCTION auth_custom.enqueue_user_sync_on_insert();

CREATE TRIGGER enqueue_user_sync_on_update
    AFTER UPDATE ON auth.users
    FOR EACH ROW EXECUTE FUNCTION auth_custom.enqueue_user_sync_on_update();

CREATE TRIGGER enqueue_user_sync_on_delete
    AFTER DELETE ON auth.users
    FOR EACH ROW EXECUTE FUNCTION auth_custom.enqueue_user_sync_on_delete();

GRANT INSERT ON auth_custom.river_job TO trigger_user;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA auth_custom TO trigger_user;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS enqueue_user_sync_on_insert ON auth.users;
DROP TRIGGER IF EXISTS enqueue_user_sync_on_update ON auth.users;
DROP TRIGGER IF EXISTS enqueue_user_sync_on_delete ON auth.users;

DROP FUNCTION IF EXISTS auth_custom.enqueue_user_sync_on_insert();
DROP FUNCTION IF EXISTS auth_custom.enqueue_user_sync_on_update();
DROP FUNCTION IF EXISTS auth_custom.enqueue_user_sync_on_delete();

REVOKE INSERT ON auth_custom.river_job FROM trigger_user;
REVOKE USAGE, SELECT ON ALL SEQUENCES IN SCHEMA auth_custom FROM trigger_user;

-- +goose StatementEnd
