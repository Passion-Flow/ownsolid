CREATE TABLE op_fixture_tenants(id uuid PRIMARY KEY,enabled boolean NOT NULL);
CREATE TABLE op_fixture_installations(
 tenant_id uuid NOT NULL REFERENCES op_fixture_tenants(id),workspace_id uuid NOT NULL,id uuid NOT NULL,
 revision bigint NOT NULL CHECK(revision>0),enabled boolean NOT NULL,
 PRIMARY KEY(tenant_id,id),UNIQUE(tenant_id,workspace_id,id)
);
CREATE TABLE op_fixture_principals(
 tenant_id uuid NOT NULL REFERENCES op_fixture_tenants(id),user_id uuid NOT NULL,
 subject varchar(43) COLLATE "C" NOT NULL CHECK(subject~'^[A-Za-z0-9_-]{43}$'),
 auth_epoch bigint NOT NULL CHECK(auth_epoch>0),enabled boolean NOT NULL,password_digest bytea NOT NULL CHECK(octet_length(password_digest)=32),
 PRIMARY KEY(tenant_id,user_id),UNIQUE(tenant_id,subject)
);
CREATE TABLE op_fixture_memberships(
 tenant_id uuid NOT NULL,workspace_id uuid NOT NULL,user_id uuid NOT NULL,authority_id uuid NOT NULL,
 PRIMARY KEY(tenant_id,workspace_id,user_id),UNIQUE(tenant_id,workspace_id,user_id,authority_id),
 FOREIGN KEY(tenant_id,user_id) REFERENCES op_fixture_principals(tenant_id,user_id)
);
CREATE TABLE op_fixture_clients(
 tenant_id uuid NOT NULL,client_id varchar(256) COLLATE "C" NOT NULL,workspace_id uuid NOT NULL,installation_id uuid NOT NULL,
 secret_digest bytea NOT NULL CHECK(octet_length(secret_digest)=32),revision bigint NOT NULL CHECK(revision>0),redirect_uri text NOT NULL,enabled boolean NOT NULL,
 PRIMARY KEY(tenant_id,client_id),FOREIGN KEY(tenant_id,workspace_id,installation_id) REFERENCES op_fixture_installations(tenant_id,workspace_id,id)
);
CREATE TABLE op_fixture_sessions(
 tenant_id uuid NOT NULL,id uuid NOT NULL,user_id uuid NOT NULL,auth_epoch bigint NOT NULL,
 token_digest bytea NOT NULL CHECK(octet_length(token_digest)=32),created_at timestamptz NOT NULL,expires_at timestamptz NOT NULL,revoked_at timestamptz,
 PRIMARY KEY(tenant_id,id),UNIQUE(tenant_id,id,user_id),FOREIGN KEY(tenant_id,user_id) REFERENCES op_fixture_principals(tenant_id,user_id),CHECK(expires_at>created_at)
);
CREATE TABLE op_fixture_requests(
 tenant_id uuid NOT NULL,id varchar(46) COLLATE "C" NOT NULL CHECK(id~'^ar_[A-Za-z0-9_-]{43}$'),
 client_id varchar(256) COLLATE "C" NOT NULL,client_revision bigint NOT NULL,installation_revision bigint NOT NULL,workspace_id uuid NOT NULL,installation_id uuid NOT NULL,
 browser_digest bytea NOT NULL CHECK(octet_length(browser_digest)=32),csrf_digest bytea CHECK(octet_length(csrf_digest)=32),
 redirect_uri text NOT NULL,state text NOT NULL,nonce text NOT NULL,challenge varchar(43) COLLATE "C" NOT NULL,scope text NOT NULL CHECK(scope='openid'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),expires_at timestamptz NOT NULL,
 user_id uuid,session_id uuid,auth_epoch bigint,subject varchar(43) COLLATE "C",membership_authority uuid,approved_at timestamptz,
 code_digest bytea CHECK(octet_length(code_digest)=32),code_expires_at timestamptz,consumed_at timestamptz,
 PRIMARY KEY(tenant_id,id),UNIQUE(tenant_id,code_digest),FOREIGN KEY(tenant_id,client_id) REFERENCES op_fixture_clients(tenant_id,client_id),
 FOREIGN KEY(tenant_id,user_id) REFERENCES op_fixture_principals(tenant_id,user_id),FOREIGN KEY(tenant_id,session_id,user_id) REFERENCES op_fixture_sessions(tenant_id,id,user_id),
 CHECK((approved_at IS NULL AND user_id IS NULL AND session_id IS NULL AND auth_epoch IS NULL AND subject IS NULL AND membership_authority IS NULL) OR
 (approved_at IS NOT NULL AND user_id IS NOT NULL AND session_id IS NOT NULL AND auth_epoch IS NOT NULL AND subject IS NOT NULL AND membership_authority IS NOT NULL)),
 CHECK((code_digest IS NULL)=(code_expires_at IS NULL)),CHECK(consumed_at IS NULL OR code_digest IS NOT NULL),CHECK(expires_at>created_at)
);
CREATE INDEX op_fixture_pending ON op_fixture_requests(tenant_id,client_id,expires_at) WHERE consumed_at IS NULL;
CREATE TABLE op_fixture_tokens(
 tenant_id uuid NOT NULL,token_digest bytea NOT NULL CHECK(octet_length(token_digest)=32),request_id varchar(46) COLLATE "C" NOT NULL,
 client_id varchar(256) COLLATE "C" NOT NULL,client_revision bigint NOT NULL,installation_revision bigint NOT NULL,workspace_id uuid NOT NULL,installation_id uuid NOT NULL,
 user_id uuid NOT NULL,session_id uuid NOT NULL,auth_epoch bigint NOT NULL,subject varchar(43) COLLATE "C" NOT NULL,membership_authority uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),expires_at timestamptz NOT NULL,revoked_at timestamptz,
 PRIMARY KEY(tenant_id,token_digest),FOREIGN KEY(tenant_id,request_id) REFERENCES op_fixture_requests(tenant_id,id),
 FOREIGN KEY(tenant_id,client_id) REFERENCES op_fixture_clients(tenant_id,client_id),FOREIGN KEY(tenant_id,session_id,user_id) REFERENCES op_fixture_sessions(tenant_id,id,user_id),
 CHECK(expires_at>created_at)
);
CREATE TABLE op_fixture_events(
 id uuid PRIMARY KEY,tenant_id uuid NOT NULL REFERENCES op_fixture_tenants(id),client_id varchar(256) COLLATE "C" NOT NULL,
 action text NOT NULL CHECK(action IN ('code_issued','code_redeemed','token_revoked')),request_id varchar(46) COLLATE "C" NOT NULL,occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(tenant_id,request_id) REFERENCES op_fixture_requests(tenant_id,id)
);
