-- How control paces the agents of this cell at runtime (ARC-007, ADR 0013). There is one
-- row per cell; the seeded row matches wire.DefaultPacing, and the bounds match
-- wire.Pacing.Validate, which agents apply again on their side.
CREATE TABLE agent_pacing (
 site_id varchar(32) NOT NULL DEFAULT local_site_id(),
 cell_id varchar(32) NOT NULL DEFAULT local_cell_id(),
 heartbeat_seconds integer NOT NULL CHECK(heartbeat_seconds BETWEEN 5 AND 120),
 jitter_percent integer NOT NULL CHECK(jitter_percent BETWEEN 0 AND 50),
 reconnect_base_seconds integer NOT NULL CHECK(reconnect_base_seconds BETWEEN 1 AND 60),
 reconnect_max_seconds integer NOT NULL CHECK(reconnect_max_seconds BETWEEN reconnect_base_seconds AND 900),
 version bigint NOT NULL CHECK(version>=1),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(site_id,cell_id)
);
INSERT INTO agent_pacing(heartbeat_seconds,jitter_percent,reconnect_base_seconds,reconnect_max_seconds,version) VALUES(10,10,1,60,1);
