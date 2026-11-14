-- Candidate schema exercised with PostgreSQL in the scheduling tests.
CREATE TABLE scheduling_quota_policy (
  singleton boolean PRIMARY KEY CHECK (singleton),
  revision bigint NOT NULL CHECK (revision > 0),
  allocation jsonb NOT NULL CHECK (jsonb_typeof(allocation) = 'array')
);
