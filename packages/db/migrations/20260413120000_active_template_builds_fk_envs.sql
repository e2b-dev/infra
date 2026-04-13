-- +goose Up

-- Clean up orphaned rows (templates already deleted).
DELETE FROM public.active_template_builds
WHERE template_id NOT IN (SELECT id FROM public.envs);

-- Ensure cascade cleanup when a template is deleted.
ALTER TABLE public.active_template_builds
    ADD CONSTRAINT fk_active_template_builds_envs
    FOREIGN KEY (template_id) REFERENCES public.envs(id) ON DELETE CASCADE;

-- +goose Down

ALTER TABLE public.active_template_builds
    DROP CONSTRAINT IF EXISTS fk_active_template_builds_envs;
