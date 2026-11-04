-- Explicit isolated qualification only. Runtime operators remain up-only.
ALTER TABLE model_preparation_reviews DROP CONSTRAINT model_review_approval_identity_required;
