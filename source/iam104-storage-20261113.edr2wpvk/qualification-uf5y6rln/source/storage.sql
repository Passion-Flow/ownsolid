CREATE TABLE saml_identity_contexts (
 tenant_id uuid NOT NULL,
 connector_id uuid NOT NULL,
 id uuid NOT NULL DEFAULT gen_random_uuid(),
 issuer text COLLATE "C" NOT NULL CHECK(char_length(issuer) BETWEEN 1 AND 1024 AND octet_length(issuer)<=4096),
 sp_entity text COLLATE "C" NOT NULL CHECK(char_length(sp_entity) BETWEEN 1 AND 1024 AND octet_length(sp_entity)<=4096),
 name_id_format text COLLATE "C" NOT NULL CHECK(name_id_format='urn:oasis:names:tc:SAML:2.0:nameid-format:persistent'),
 metadata_digest bytea NOT NULL CHECK(octet_length(metadata_digest)=32),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 enabled boolean NOT NULL DEFAULT true,
 PRIMARY KEY(tenant_id,connector_id),UNIQUE(tenant_id,id)
);
CREATE TABLE saml_persistent_identities (
 tenant_id uuid NOT NULL,
 context_id uuid NOT NULL,
 id uuid NOT NULL DEFAULT gen_random_uuid(),
 name_id_value text COLLATE "C" NOT NULL CHECK(char_length(name_id_value) BETWEEN 1 AND 256 AND octet_length(name_id_value)<=1024),
 PRIMARY KEY(tenant_id,id),UNIQUE(tenant_id,context_id,name_id_value),
 FOREIGN KEY(tenant_id,context_id) REFERENCES saml_identity_contexts(tenant_id,id)
);
CREATE FUNCTION saml_identity_context_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(OLD.tenant_id,OLD.connector_id,OLD.id,OLD.issuer,OLD.sp_entity,OLD.name_id_format)
    IS DISTINCT FROM ROW(NEW.tenant_id,NEW.connector_id,NEW.id,NEW.issuer,NEW.sp_entity,NEW.name_id_format) THEN
  RAISE EXCEPTION 'SAML_IDENTITY_CONTEXT_IMMUTABLE' USING ERRCODE='23514';
 END IF;
 IF NEW.revision<OLD.revision OR
    (ROW(OLD.enabled,OLD.metadata_digest) IS DISTINCT FROM ROW(NEW.enabled,NEW.metadata_digest) AND NEW.revision<=OLD.revision) THEN
  RAISE EXCEPTION 'SAML_IDENTITY_CONTEXT_REVISION_REQUIRED' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER saml_identity_context_immutable BEFORE UPDATE ON saml_identity_contexts
FOR EACH ROW EXECUTE FUNCTION saml_identity_context_immutable();
CREATE FUNCTION saml_persistent_identity_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD IS DISTINCT FROM NEW THEN
  RAISE EXCEPTION 'SAML_PERSISTENT_IDENTITY_IMMUTABLE' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER saml_persistent_identity_immutable BEFORE UPDATE ON saml_persistent_identities
FOR EACH ROW EXECUTE FUNCTION saml_persistent_identity_immutable();
