package install

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

const (
	restoreIntentName = "restore-intent.json"
	restoreLoading    = "loading"
	restorePending    = "pending"
	restoreServing    = "serving"
	restoreRollback   = "rollback"
	// Serialize the forward and reverse rename transactions, including a
	// PostgreSQL request left behind after the installer process is killed.
	restoreSwapLock = 6750471102516012
)

var restoreSuffix = regexp.MustCompile(`^[a-f0-9]{24}$`)

type restoreIntent struct {
	Version        int    `json:"version"`
	Site           string `json:"site_sha256"`
	Phase          string `json:"phase"`
	Suffix         string `json:"suffix"`
	Cluster        int64  `json:"cluster"`
	Original       uint32 `json:"original_oid"`
	Incoming       uint32 `json:"incoming_oid"`
	Fresh          bool   `json:"fresh"`
	PITRActivation string `json:"pitr_activation,omitempty"`
}

func (i restoreIntent) incomingName() string { return "ownsolid_restore_" + i.Suffix }
func (i restoreIntent) previousName() string { return "ownsolid_before_restore_" + i.Suffix }
func (i restoreIntent) failedName() string   { return "ownsolid_failed_restore_" + i.Suffix }

func restoreSite(c Config) (string, error) {
	// Recovery may need a longer wait without changing the running installation.
	c.StepSeconds, c.BackupSeconds = 0, 0
	raw, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("INSTALL_RESTORE_SITE_INVALID: %w", err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}

func readRestoreIntent(c Config) (*restoreIntent, error) {
	path := filepath.Join(c.InstallRoot, restoreIntentName)
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	raw, err := bundle.ReadProtectedConfiguration(path)
	var i restoreIntent
	if err != nil || wire.StrictJSON(raw, &i) != nil ||
		i.Version != 1 || !restoreSuffix.MatchString(i.Suffix) ||
		i.Cluster == 0 || i.Original == 0 || i.Incoming == 0 || i.Original == i.Incoming ||
		(i.PITRActivation != "" && !validPITRActivation(i.PITRActivation)) ||
		(i.Phase != restoreLoading && i.Phase != restorePending && i.Phase != restoreServing && i.Phase != restoreRollback) {
		return nil, errors.New("INSTALL_RESTORE_INTENT_INVALID: preserve the recovery record and the databases for inspection")
	}
	site, err := restoreSite(c)
	if err != nil {
		return nil, err
	}
	if i.Site != site {
		return nil, errors.New("INSTALL_RESTORE_SITE_CHANGED: run recover-restore with the original site configuration")
	}
	return &i, nil
}

func restorePendingError(c Config) error {
	if _, err := os.Lstat(filepath.Join(c.InstallRoot, restoreIntentName)); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return errors.New("INSTALL_RESTORE_PENDING: run recover-restore with the original site configuration")
}

// RecoverRestore reconciles a pending restore from PostgreSQL's actual database
// identities and the saved writer-admission decision. Set rollback to put the
// original back and retain the candidate. Recovery needs no backup files.
func RecoverRestore(ctx context.Context, c Config, runner StreamRunner, rollback bool, out io.Writer) error {
	if os.Geteuid() != 0 {
		return errors.New("INSTALL_ROOT_REQUIRED: recover-restore requires root access to the installation")
	}
	unlock, err := lockRestoreOperation(c)
	if err != nil {
		return err
	}
	defer unlock()
	intent, err := readRestoreIntent(c)
	if err != nil {
		return err
	}
	if intent == nil {
		fmt.Fprintln(out, "RESTORE_NO_RECOVERY_PENDING")
		return nil
	}
	if err := checkBoundOperation(c); err != nil {
		return err
	}
	if err := checkInstalledProject(c); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.BackupSeconds)*time.Second)
	defer cancel()
	o := newOperation(c, runner, out)
	if err := o.recoverRestore(ctx, *intent, rollback); err != nil {
		o.record("restore", "failed", err.Error())
		return retryWith(err, "recover-restore")
	}
	return nil
}

func (o *operation) recoverRestore(ctx context.Context, i restoreIntent, rollback bool) error {
	if i.Phase == restoreLoading {
		if err := o.runSteps(ctx, []struct {
			name string
			run  func(context.Context) error
		}{{"database", o.database}}); err != nil {
			return err
		}
		return o.recoverRestoreLoad(ctx, i)
	}
	if err := o.runSteps(ctx, []struct {
		name string
		run  func(context.Context) error
	}{{"database", o.database}, {"telemetry", o.telemetry}}); err != nil {
		return err
	}
	catalog, err := o.restoreCatalog(ctx, i)
	if err != nil {
		return err
	}
	switch {
	case i.Phase == restoreRollback && (catalog == i.before() || catalog == i.swapped() || catalog == i.rolledBack()):
		return o.restoreOriginal(ctx, i)
	case (i.Phase == restorePending && catalog == i.before()) || (rollback && catalog == i.swapped()):
		return o.beginRestoreRollback(ctx, i)
	case catalog == i.swapped():
		if err := o.finishRestored(ctx, i); err != nil {
			return err
		}
		o.record("restore", "complete", i.previousName())
		return nil
	default:
		return restoreIdentityError()
	}
}

func (o *operation) failRestoreLoad(ctx context.Context, i restoreIntent, cause error) error {
	if ctx.Err() != nil {
		return restoreRecoveryRequired(cause)
	}
	catalog, err := o.restoreCatalog(ctx, i)
	if err != nil || catalog != i.before() {
		return restoreRecoveryRequired(errors.Join(cause, restoreIdentityError(), err))
	}
	if err := o.dropDatabase(ctx, i.incomingName()); err != nil {
		return restoreRecoveryRequired(errors.Join(cause, err))
	}
	return errors.Join(cause, o.clearRestoreIntent())
}

func (o *operation) recoverRestoreLoad(ctx context.Context, i restoreIntent) error {
	catalog, err := o.restoreCatalog(ctx, i)
	if err != nil {
		return err
	}
	retained := ""
	switch catalog {
	case i.before():
		if err := o.renameForRestore(ctx, i, true); err != nil {
			return err
		}
		retained = i.failedName()
	case i.rolledBack():
		retained = i.failedName()
	case (restoreCatalog{cluster: i.Cluster, current: i.Original}):
		// Cleanup completed before the installer could remove the record.
	default:
		return restoreIdentityError()
	}
	if err := o.clearRestoreIntent(); err != nil {
		return err
	}
	o.record("restore", "rolled-back", retained)
	fmt.Fprintln(o.out, "RESTORE_LOAD_ROLLED_BACK")
	if retained != "" {
		fmt.Fprintf(o.out, "Unverified candidate retained as %s\n", retained)
	}
	return nil
}

func (o *operation) finishRestored(ctx context.Context, i restoreIntent) error {
	resumeServing := i.Phase == restoreServing
	if !resumeServing {
		if err := o.prepareRestored(ctx, i.PITRActivation); err != nil {
			return o.failRestore(ctx, i, err)
		}
		i.Phase = restoreServing
		if err := writeReleaseRecord(o.config, restoreIntentName, i); err != nil {
			return restoreRecoveryRequired(err)
		}
	}
	if err := o.startRestoreWriter(ctx); err != nil {
		if resumeServing {
			// This candidate may have served for some time before recovery.
			// Leave its writes on the active database while the operator retries.
			return restoreRecoveryRequired(err)
		}
		return o.failRestore(ctx, i, err)
	}
	if err := o.clearRestoreIntent(); err != nil {
		return err
	}
	fmt.Fprintf(o.out, "RESTORE_COMPLETE previous_database=%s\n", i.previousName())
	fmt.Fprintln(o.out, "Everyone signs in again and node agents join again: restoring revokes sessions and node credentials.")
	return nil
}

func (o *operation) failRestore(ctx context.Context, i restoreIntent, cause error) error {
	// A timed-out Docker client can leave work in progress. Keep its target
	// stable and let recovery reconcile it before another writer starts.
	if ctx.Err() != nil || errors.Is(cause, context.DeadlineExceeded) || errors.Is(cause, context.Canceled) {
		return restoreRecoveryRequired(cause)
	}
	recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(o.config.StepSeconds)*time.Second)
	defer cancel()
	if err := o.beginRestoreRollback(recovery, i); err != nil {
		return restoreRecoveryRequired(errors.Join(cause, err))
	}
	return cause
}

func (o *operation) beginRestoreRollback(ctx context.Context, i restoreIntent) error {
	i.Phase = restoreRollback
	if err := writeReleaseRecord(o.config, restoreIntentName, i); err != nil {
		return err
	}
	return o.restoreOriginal(ctx, i)
}

func (o *operation) restoreOriginal(ctx context.Context, i restoreIntent) error {
	if err := o.stopControl(ctx); err != nil {
		return wrapStep("roll-back", err)
	}
	if err := o.renameForRestore(ctx, i, true); err != nil {
		return &StepError{Step: "roll-back", Code: "INSTALL_RESTORE_ROLLBACK_FAILED",
			Detail: fmt.Sprintf("cannot put back the original database (possible name %s): %v", i.previousName(), err)}
	}
	if !i.Fresh {
		if err := o.startRestoreWriter(ctx); err != nil {
			return err
		}
	}
	if err := o.clearRestoreIntent(); err != nil {
		return err
	}
	o.record("restore", "rolled-back", i.failedName())
	fmt.Fprintf(o.out, "RESTORE_ROLLED_BACK candidate_database=%s\n", i.failedName())
	return nil
}

func (o *operation) startRestoreWriter(ctx context.Context) error {
	return o.runSteps(ctx, []struct {
		name string
		run  func(context.Context) error
	}{{"control", o.control}, {"health", o.health}})
}

func (o *operation) clearRestoreIntent() error {
	if err := os.Remove(filepath.Join(o.config.InstallRoot, restoreIntentName)); err != nil {
		return restoreRecoveryRequired(err)
	}
	if err := syncDirectory(o.config.InstallRoot); err != nil {
		return restoreRecoveryRequired(err)
	}
	return nil
}

func restoreRecoveryRequired(cause error) error {
	return fmt.Errorf("INSTALL_RESTORE_PENDING: run recover-restore with the original site configuration: %w", cause)
}

func restoreIdentityError() error {
	return errors.New("INSTALL_RESTORE_IDENTITY_CHANGED: PostgreSQL does not match the recovery record; inspect the cluster and database identities before retrying")
}

// Zero in a name slot means that name is absent, never an unknown database.
type restoreCatalog struct {
	cluster                             int64
	current, incoming, previous, failed uint32
}

func (i restoreIntent) before() restoreCatalog {
	return restoreCatalog{cluster: i.Cluster, current: i.Original, incoming: i.Incoming}
}

func (i restoreIntent) swapped() restoreCatalog {
	return restoreCatalog{cluster: i.Cluster, current: i.Incoming, previous: i.Original}
}

func (i restoreIntent) rolledBack() restoreCatalog {
	return restoreCatalog{cluster: i.Cluster, current: i.Original, failed: i.Incoming}
}

func (c restoreCatalog) key() string {
	return fmt.Sprintf("%d|%d|%d|%d|%d", c.cluster, c.current, c.incoming, c.previous, c.failed)
}

// The transaction and the Go caller use the same catalog query. All names are
// constants or derived from a validated random hexadecimal suffix.
func (i restoreIntent) catalogSQL() string {
	return fmt.Sprintf(`SELECT concat_ws('|', system_identifier,
    coalesce((SELECT oid FROM pg_catalog.pg_database WHERE datname = '%s'), 0),
    coalesce((SELECT oid FROM pg_catalog.pg_database WHERE datname = '%s'), 0),
    coalesce((SELECT oid FROM pg_catalog.pg_database WHERE datname = '%s'), 0),
    coalesce((SELECT oid FROM pg_catalog.pg_database WHERE datname = '%s'), 0))
FROM pg_catalog.pg_control_system()`, databaseName, i.incomingName(), i.previousName(), i.failedName())
}

func (o *operation) restoreCatalog(ctx context.Context, i restoreIntent) (restoreCatalog, error) {
	raw, err := o.query(ctx, maintenanceDatabase, i.catalogSQL())
	if err != nil {
		return restoreCatalog{}, err
	}
	var c restoreCatalog
	parts := strings.Split(raw, "|")
	if len(parts) != 5 {
		return c, restoreIdentityError()
	}
	c.cluster, err = strconv.ParseInt(parts[0], 10, 64)
	if err != nil || c.cluster == 0 {
		return c, restoreIdentityError()
	}
	for n, oid := range []*uint32{&c.current, &c.incoming, &c.previous, &c.failed} {
		value, err := strconv.ParseUint(parts[n+1], 10, 32)
		if err != nil {
			return c, restoreIdentityError()
		}
		*oid = uint32(value)
	}
	return c, nil
}

func (i restoreIntent) renameSQL(reverse bool) (string, restoreCatalog) {
	before, swapped, rolledBack := i.before(), i.swapped(), i.rolledBack()
	terminate := func(oids string) string {
		return "PERFORM pg_terminate_backend(pid) FROM pg_catalog.pg_stat_activity WHERE datid IN (" + oids + ");"
	}
	both := terminate(fmt.Sprintf("%d,%d", i.Original, i.Incoming))
	forward := fmt.Sprintf(`%s ALTER DATABASE "%s" RENAME TO "%s"; ALTER DATABASE "%s" RENAME TO "%s";`,
		both, databaseName, i.previousName(), i.incomingName(), databaseName)
	branches := fmt.Sprintf("IF placement = '%s' THEN %s", before.key(), forward)
	target := swapped
	if reverse {
		fromBefore := fmt.Sprintf(`%s ALTER DATABASE "%s" RENAME TO "%s";`,
			terminate(fmt.Sprint(i.Incoming)), i.incomingName(), i.failedName())
		branches = fmt.Sprintf("IF placement = '%s' THEN %s", before.key(), fromBefore)
		if i.Phase != restoreLoading {
			fromSwapped := fmt.Sprintf(`%s ALTER DATABASE "%s" RENAME TO "%s"; ALTER DATABASE "%s" RENAME TO "%s";`,
				both, databaseName, i.failedName(), i.previousName(), databaseName)
			branches += fmt.Sprintf(" ELSIF placement = '%s' THEN %s", swapped.key(), fromSwapped)
		}
		target = rolledBack
	}
	return fmt.Sprintf(`BEGIN ISOLATION LEVEL READ COMMITTED;
SET LOCAL synchronous_commit = on;
SELECT pg_advisory_xact_lock(%d);
DO $restore$
DECLARE placement text := (%s);
BEGIN
    %s
    ELSIF placement IS DISTINCT FROM '%s' THEN
        RAISE EXCEPTION 'INSTALL_RESTORE_IDENTITY_CHANGED';
    END IF;
END;
$restore$;
COMMIT;`, restoreSwapLock, i.catalogSQL(), branches, target.key()), target
}

func (o *operation) renameForRestore(ctx context.Context, i restoreIntent, reverse bool) error {
	if i.Phase == restoreLoading && !reverse {
		return restoreIdentityError()
	}
	statement, target := i.renameSQL(reverse)
	var err error
	for attempt := 0; attempt < swapAttempts; attempt++ {
		_, err = o.query(ctx, maintenanceDatabase, statement)
		if err == nil {
			return nil
		}
		// The command can fail after COMMIT. Read PostgreSQL, not the client
		// result, before retrying or taking a different recovery path.
		catalog, readErr := o.restoreCatalog(ctx, i)
		if readErr == nil {
			if catalog == target {
				return nil
			}
			if catalog != i.before() && !(reverse && i.Phase != restoreLoading && catalog == i.swapped()) {
				return restoreIdentityError()
			}
		}
		if attempt+1 < swapAttempts {
			select {
			case <-time.After(swapRetryDelay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return err
}
