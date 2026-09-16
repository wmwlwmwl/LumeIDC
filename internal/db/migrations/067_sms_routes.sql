ALTER TABLE sms_outbox ADD COLUMN IF NOT EXISTS route_fingerprint TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS sms_outbox_route_fingerprint_idx ON sms_outbox(route_fingerprint);
