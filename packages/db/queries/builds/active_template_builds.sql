-- name: CreateActiveTemplateBuild :exec
INSERT INTO public.active_template_builds (
    build_id,
    team_id,
    template_id,
    tags
) VALUES (
    @build_id,
    @team_id,
    @template_id,
    @tags::text[]
);
