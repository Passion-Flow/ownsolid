-- M01+M02 ledger version 6. Immutable membership incarnation identity.
-- Existing memberships retain tenant/workspace/user keys, roles and timestamps.
-- No credentials, sessions, account/mail/license/audit rows are changed.
ALTER TABLE workspace_memberships ADD COLUMN authority_id uuid NOT NULL DEFAULT gen_random_uuid();
CREATE UNIQUE INDEX account_membership_authority_idx ON workspace_memberships(authority_id);
