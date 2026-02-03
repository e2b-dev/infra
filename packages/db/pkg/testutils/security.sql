-- name: GetRowLevelSecurity :many
select n.nspname as namespace_name, c.relname as table_name, c.relrowsecurity as row_level_security
from pg_catalog.pg_class c
join pg_catalog.pg_namespace n on c.relnamespace = n.oid
join information_schema.tables t on t.table_name = c.relname and t.table_schema = n.nspname
where t.table_type = 'BASE TABLE';
