-- Preserve historical JSON text and allow new opaque payloads, including empty bytes.
-- Apply before new writers start; old JSON writers cannot run against this schema.
ALTER TABLE event_outbox ALTER COLUMN payload DROP DEFAULT;
ALTER TABLE event_outbox ALTER COLUMN payload TYPE BYTEA USING convert_to(payload::text, 'UTF8');
ALTER TABLE event_outbox ALTER COLUMN payload SET DEFAULT ''::bytea;
