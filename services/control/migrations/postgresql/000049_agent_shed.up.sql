-- An operator can tell control to refuse agent traffic, lowest priority first (ARC-016, ADR 0043). The
-- level lives beside the pacing of the cell, so it survives a restart and reaches every gateway.
ALTER TABLE agent_pacing
 ADD COLUMN shed_level text NOT NULL DEFAULT 'none' CHECK (shed_level IN ('none','bulk','telemetry','heartbeats')),
 ADD COLUMN shed_version bigint NOT NULL DEFAULT 1 CHECK (shed_version>=1),
 ADD COLUMN shed_updated_at timestamptz NOT NULL DEFAULT clock_timestamp();
