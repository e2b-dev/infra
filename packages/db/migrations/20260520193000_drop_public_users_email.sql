-- +goose Up
-- +goose StatementBegin

ALTER TABLE public.users DROP CONSTRAINT IF EXISTS users_email_key;
ALTER TABLE public.users DROP COLUMN IF EXISTS email;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE public.users ADD COLUMN IF NOT EXISTS email text;

UPDATE public.users u
SET email = au.email
FROM auth.users au
WHERE au.id = u.id
  AND u.email IS NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'users_email_key'
          AND conrelid = 'public.users'::regclass
    ) THEN
        ALTER TABLE public.users ADD CONSTRAINT users_email_key UNIQUE (email);
    END IF;
END $$;

-- +goose StatementEnd
