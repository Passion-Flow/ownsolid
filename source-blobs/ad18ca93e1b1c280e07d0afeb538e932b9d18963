ALTER TABLE model_preparations
    DROP CHECK model_preparations_chk_12,
    ADD COLUMN review_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin,
    ADD COLUMN derived_model_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin,
    ADD CONSTRAINT model_preparation_review_state CHECK(state IN('queued','preparing','running','cancelling','recovery_required','quality_failed','awaiting_review','approved','rejected','cancelled','failed')),
    ADD CONSTRAINT model_preparation_review_presence CHECK((state='approved' AND review_id IS NOT NULL AND derived_model_id IS NOT NULL) OR (state='rejected' AND review_id IS NOT NULL AND derived_model_id IS NULL) OR (state NOT IN('approved','rejected') AND review_id IS NULL AND derived_model_id IS NULL)),
    ADD CONSTRAINT model_preparation_review_scope FOREIGN KEY(tenant_id,workspace_id,review_id) REFERENCES model_preparation_reviews(tenant_id,workspace_id,id),
    ADD CONSTRAINT model_preparation_derived_scope FOREIGN KEY(tenant_id,workspace_id,derived_model_id) REFERENCES model_versions(tenant_id,workspace_id,id),
    ADD CONSTRAINT model_preparation_review_id_canonical CHECK(review_id IS NULL OR REGEXP_LIKE(review_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
    ADD CONSTRAINT model_preparation_derived_id_canonical CHECK(derived_model_id IS NULL OR REGEXP_LIKE(derived_model_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c'));
