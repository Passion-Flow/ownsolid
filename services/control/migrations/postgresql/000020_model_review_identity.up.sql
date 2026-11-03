-- Preserve immutable review provenance. Existing invalid approvals refuse upgrade;
-- migration never invents a model identity or human review decision.
ALTER TABLE model_preparation_reviews
 ADD CONSTRAINT model_review_approval_identity_required
 CHECK((decision='approve' AND model_id IS NOT NULL AND model_id=artifact_id AND length(model_name)>0)
    OR (decision='reject' AND model_id IS NULL AND model_name=''));
