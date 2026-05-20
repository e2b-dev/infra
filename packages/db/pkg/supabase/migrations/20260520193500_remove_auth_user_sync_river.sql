DROP TRIGGER IF EXISTS enqueue_user_sync_on_insert ON auth.users;
DROP TRIGGER IF EXISTS enqueue_user_sync_on_update ON auth.users;
DROP TRIGGER IF EXISTS enqueue_user_sync_on_delete ON auth.users;

DROP FUNCTION IF EXISTS auth_custom.enqueue_user_sync_on_insert();
DROP FUNCTION IF EXISTS auth_custom.enqueue_user_sync_on_update();
DROP FUNCTION IF EXISTS auth_custom.enqueue_user_sync_on_delete();

DROP TABLE IF EXISTS auth_custom.river_job CASCADE;
DROP TABLE IF EXISTS auth_custom.river_leader CASCADE;
DROP TABLE IF EXISTS auth_custom.river_migration CASCADE;
DROP TABLE IF EXISTS auth_custom.river_queue CASCADE;
DROP TABLE IF EXISTS auth_custom.river_workflow CASCADE;
