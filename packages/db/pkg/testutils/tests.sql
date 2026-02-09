-- name: GetRowLevelSecurity :many
select
    c.relkind::text as kind,
    n.nspname as namespace_name,
    c.relname as table_name,
    c.relrowsecurity as row_level_security,
    c.reloptions::text[] as options
from pg_catalog.pg_class c
join pg_catalog.pg_namespace n on c.relnamespace = n.oid
where c.relkind in (
    'r', -- ordinary table
    'v', -- view
    'm'  -- materialized view
);
