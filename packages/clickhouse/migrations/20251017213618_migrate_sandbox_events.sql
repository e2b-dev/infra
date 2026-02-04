-- +goose Up

-- Migrate sandbox created events
ALTER TABLE sandbox_events_local
UPDATE
    version = 'v2',
    type    = 'sandbox.lifecycle.created'
WHERE event_category = 'lifecycle' AND event_label = 'create' AND version = 'v1';

-- Migrate sandbox killed events
ALTER TABLE sandbox_events_local
UPDATE
    version = 'v2',
    type    = 'sandbox.lifecycle.killed'
WHERE event_category = 'lifecycle' AND event_label = 'kill' AND version = 'v1';

-- Migrate sandbox paused events
ALTER TABLE sandbox_events_local
UPDATE
    version = 'v2',
    type    = 'sandbox.lifecycle.paused'
WHERE event_category = 'lifecycle' AND event_label = 'pause' AND version = 'v1';

-- Migrate sandbox resumed events
ALTER TABLE sandbox_events_local
UPDATE
    version = 'v2',
    type    = 'sandbox.lifecycle.resumed'
WHERE event_category = 'lifecycle' AND event_label = 'resume' AND version = 'v1';

-- Migrate sandbox updated events
ALTER TABLE sandbox_events_local
UPDATE
    version = 'v2',
    type    = 'sandbox.lifecycle.updated'
WHERE event_category = 'lifecycle' AND event_label = 'update' AND version = 'v1';
