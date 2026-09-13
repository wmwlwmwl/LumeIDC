ALTER TABLE services
    ADD COLUMN IF NOT EXISTS host_snapshot JSONB;
