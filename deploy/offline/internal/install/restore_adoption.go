package install

import "context"

const restoreAdoptionSetting = "ownsolid.restore_adoption"
const restoreAdoptionBinding = `format('v1/%s/%s/%s', system_identifier,
    d.oid, 'public.installation_state'::regclass::oid)
FROM pg_catalog.pg_database AS d
CROSS JOIN pg_catalog.pg_control_system()
WHERE d.datname = current_database()`

// The adoption record belongs to the restored database. Renaming a failed
// candidate aside cannot grant its authority to the database put back in place.
func (o *operation) recordRestoreAdoption(ctx context.Context) error {
	// The host may persist writer admission immediately after this command.
	// Flush this marker and all preceding database changes before it can.
	const statement = `BEGIN;
SET LOCAL synchronous_commit = on;
DO $adopt$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM public.installation_state WHERE id = 1 AND bootstrap_complete) THEN
        RAISE EXCEPTION 'the restored database has no completed administrator bootstrap';
    END IF;
    EXECUTE format('ALTER DATABASE %I SET ` + restoreAdoptionSetting + ` TO %L',
        current_database(), (SELECT ` + restoreAdoptionBinding + `));
END;
$adopt$;
COMMIT;`
	if _, err := o.query(ctx, databaseName, statement); err != nil {
		return &StepError{Code: "INSTALL_RESTORE_ADOPTION_FAILED", Detail: err.Error()}
	}
	return nil
}

// Read the database-owned default directly. A session value or role override
// must never make an unadopted database look like a verified restore.
func (i *installer) hasRestoreAdoption(ctx context.Context) (bool, error) {
	const statement = `SELECT EXISTS (
    SELECT 1 FROM public.installation_state AS state
    JOIN pg_catalog.pg_db_role_setting AS settings
      ON settings.setdatabase = (SELECT oid FROM pg_catalog.pg_database WHERE datname = current_database())
     AND settings.setrole = 0
    WHERE state.id = 1 AND state.bootstrap_complete
      AND ('` + restoreAdoptionSetting + `=' || (SELECT ` + restoreAdoptionBinding + `)) = ANY(settings.setconfig)
)`
	output, err := i.compose(ctx, "exec", "--no-TTY", "postgres", "psql", "-X", "-qAt",
		"-v", "ON_ERROR_STOP=1", "-U", migratorRole, "-d", databaseName, "-c", statement)
	if err == nil {
		yes, no := containsLine(output, "t"), containsLine(output, "f")
		if yes != no {
			return yes, nil
		}
	}
	return false, &StepError{Code: "INSTALL_RESTORE_ADOPTION_CHECK_FAILED",
		Detail: "cannot verify the database's restore state: " + lastLines(output)}
}
