-- +goose Up
-- +goose StatementBegin

CREATE TABLE public.user_sync_queue (
    id            BIGSERIAL    PRIMARY KEY,
    user_id       UUID         NOT NULL,
    operation     TEXT         NOT NULL CHECK (operation IN ('upsert', 'delete')),
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    locked_at     TIMESTAMPTZ  NULL,
    lock_owner    TEXT         NULL,
    attempt_count INT          NOT NULL DEFAULT 0,
    last_error    TEXT         NULL,
    dead_lettered_at TIMESTAMPTZ NULL
);

ALTER TABLE public.user_sync_queue ENABLE ROW LEVEL SECURITY;

CREATE INDEX auth_user_sync_queue_pending_idx
    ON public.user_sync_queue (id)
    WHERE dead_lettered_at IS NULL AND locked_at IS NULL;

CREATE INDEX auth_user_sync_queue_user_idx
    ON public.user_sync_queue (user_id);

GRANT INSERT ON public.user_sync_queue TO trigger_user;
GRANT USAGE, SELECT ON SEQUENCE public.user_sync_queue_id_seq TO trigger_user;

CREATE POLICY "Allow to create a user sync queue item"
    ON public.user_sync_queue
    AS PERMISSIVE
    FOR INSERT
    TO trigger_user
    WITH CHECK (TRUE);

-- Keep direct insert-sync and also enqueue
CREATE OR REPLACE FUNCTION public.sync_insert_auth_users_to_public_users_trigger() RETURNS TRIGGER
LANGUAGE plpgsql
AS $func$
BEGIN
    INSERT INTO public.users (id, email)
    VALUES (NEW.id, NEW.email);

    INSERT INTO public.user_sync_queue (user_id, operation)
    VALUES (NEW.id, 'upsert');

    RETURN NEW;
END;
$func$ SECURITY DEFINER SET search_path = public;

-- Keep direct update-sync and also enqueue when mirrored fields change
CREATE OR REPLACE FUNCTION public.sync_update_auth_users_to_public_users_trigger() RETURNS TRIGGER
LANGUAGE plpgsql
AS $func$
BEGIN
    UPDATE public.users
    SET email = NEW.email,
        updated_at = now()
    WHERE id = NEW.id;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'User with id % does not exist in public.users', NEW.id;
    END IF;

    IF OLD.email IS DISTINCT FROM NEW.email THEN
        INSERT INTO public.user_sync_queue (user_id, operation)
        VALUES (NEW.id, 'upsert');
    END IF;

    RETURN NEW;
END;
$func$ SECURITY DEFINER SET search_path = public;

-- Keep direct delete-sync and also enqueue
CREATE OR REPLACE FUNCTION public.sync_delete_auth_users_to_public_users_trigger() RETURNS TRIGGER
LANGUAGE plpgsql
AS $func$
BEGIN
    DELETE FROM public.users WHERE id = OLD.id;

    INSERT INTO public.user_sync_queue (user_id, operation)
    VALUES (OLD.id, 'delete');

    RETURN OLD;
END;
$func$ SECURITY DEFINER SET search_path = public;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Restore direct insert-sync
CREATE OR REPLACE FUNCTION public.sync_insert_auth_users_to_public_users_trigger() RETURNS TRIGGER
LANGUAGE plpgsql
AS $func$
BEGIN
    INSERT INTO public.users (id, email)
    VALUES (NEW.id, NEW.email);
    RETURN NEW;
END;
$func$ SECURITY DEFINER SET search_path = public;

-- Restore direct update-sync
CREATE OR REPLACE FUNCTION public.sync_update_auth_users_to_public_users_trigger() RETURNS TRIGGER
LANGUAGE plpgsql
AS $func$
BEGIN
    UPDATE public.users
    SET email = NEW.email,
        updated_at = now()
    WHERE id = NEW.id;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'User with id % does not exist in public.users', NEW.id;
    END IF;

    RETURN NEW;
END;
$func$ SECURITY DEFINER SET search_path = public;

-- Restore direct delete-sync
CREATE OR REPLACE FUNCTION public.sync_delete_auth_users_to_public_users_trigger() RETURNS TRIGGER
LANGUAGE plpgsql
AS $func$
BEGIN
    DELETE FROM public.users WHERE id = OLD.id;
    RETURN OLD;
END;
$func$ SECURITY DEFINER SET search_path = public;

REVOKE INSERT ON public.user_sync_queue FROM trigger_user;
REVOKE USAGE, SELECT ON SEQUENCE public.user_sync_queue_id_seq FROM trigger_user;

DROP POLICY IF EXISTS "Allow to create a user sync queue item" ON public.user_sync_queue;

DROP TABLE public.user_sync_queue;

-- +goose StatementEnd
