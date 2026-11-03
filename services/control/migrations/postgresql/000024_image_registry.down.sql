-- Selected isolated rollback requires empty registry state and stopped writers.
DROP TABLE image_registry_mutations;
DROP TABLE image_registry_offline_refs;
DROP TABLE image_registry_grants;
DROP TABLE image_registry_deployment_refs;
DROP TABLE image_registry_imports;
DROP TABLE image_registry_blobs;
DROP TABLE image_registry_pins;
DROP FUNCTION image_registry_pin_guard();
DROP FUNCTION image_registry_grant_guard();
DROP FUNCTION image_registry_deployment_ref_guard();
DROP FUNCTION image_registry_offline_ref_guard();
