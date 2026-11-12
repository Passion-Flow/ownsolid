package install

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"golang.org/x/sys/unix"
)

const (
	coordinatedBackupIntentName = "coordinated-backup-intent.json"
	// A lost Docker exec request may still reach PostgreSQL. Its server-side
	// deadline also bounds how long recovery waits for an unacknowledged fence.
	coordinatedFenceWindow = 10 * time.Second
)

// This profile's exact Compose definition identifies the service, mount and
// PostgreSQL health-check writers admitted by this backup. The optional Control
// CA environment adds no writer; external fleet-history state is refused separately.
const coordinatedComposeSHA256 = "3c753f781471272963ec2fcfd7b88dff19fdb484c0e7242054ca79fa817f0dca"

// The preceding shipped profile has identical writers and mounts, without the
// optional SSL_CERT_FILE environment. Keep existing backups usable before Apply.
const legacyCoordinatedComposeSHA256 = "7f7df1dbe10bea9be4beee11db34a35a667a4e66e00001c5689388396fbf4285"

var (
	errBackupPending = errors.New("INSTALL_BACKUP_RECOVERY_REQUIRED: run recover-backup with the original site configuration")
	errBackupScope   = errors.New("INSTALL_BACKUP_PROFILE_UNSUPPORTED: coordinated backup requires the shipped local Compose profile without external artifact state")
	errBackupFence   = errors.New("INSTALL_BACKUP_FENCE_CHANGED: the database or control container differs from the recorded backup state")
	containerID      = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

type coordinatedDB struct {
	Cluster int64  `json:"cluster"`
	OID     uint32 `json:"oid"`
	Limit   int    `json:"limit"`
}

type coordinatedBackupIntent struct {
	Version    int           `json:"version"`
	Site       string        `json:"site_sha256"`
	Database   coordinatedDB `json:"database"`
	ControlID  string        `json:"control_id"`
	WasUp      bool          `json:"was_up"`
	FenceUntil *time.Time    `json:"fence_until,omitempty"`
}

func coordinatedBackupPending(c Config) error {
	if _, err := os.Lstat(filepath.Join(c.InstallRoot, coordinatedBackupIntentName)); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return errBackupPending
}

// BackupCoordinated takes a local logical backup with the control writer stopped
// and PostgreSQL closed to application logins. It restores the prior service state.
func BackupCoordinated(ctx context.Context, c Config, runner StreamRunner, output string, out io.Writer) (result error) {
	if err := coordinatedBackupHost(ctx, runner); err != nil {
		return err
	}
	if err := validateUpgradeBackupRoot(c, output); err != nil {
		return err
	}
	unlock, err := lockOperation(c)
	if err != nil {
		return err
	}
	defer unlock()
	if err := checkBoundOperation(c); err != nil {
		return err
	}
	if err := checkCoordinatedProfile(c); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.BackupSeconds)*time.Second)
	defer cancel()
	o := newOperation(c, runner, out)
	original, err := o.admitCoordinatedBackup(ctx)
	if err != nil {
		return err
	}
	// A failed sync can leave a visible record, but no writer has been touched.
	if err := writeReleaseRecord(c, coordinatedBackupIntentName, original); err != nil {
		return errors.Join(errBackupPending, err)
	}
	var published string
	defer func() {
		recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(c.StepSeconds)*time.Second)
		defer cancel()
		retry := "backup-coordinated"
		if err := o.resumeCoordinatedBackup(recovery, original); err != nil {
			result = errors.Join(result, errBackupPending, err)
			retry = "recover-backup"
		}
		if result != nil {
			result = retryWith(result, retry)
			o.record("coordinated-backup", "failed", result.Error())
			return
		}
		o.record("coordinated-backup", "complete", published)
		fmt.Fprintf(out, "BACKUP_COMPLETE path=%s\n", published)
	}()

	if original.WasUp {
		if err := o.stopCoordinatedControl(ctx, original.ControlID); err != nil {
			return err
		}
	}
	until := time.Now().UTC().Add(coordinatedFenceWindow)
	original.FenceUntil = &until
	if err := writeReleaseRecord(c, coordinatedBackupIntentName, original); err != nil {
		return err
	}
	fence, stopFence := context.WithDeadline(context.WithoutCancel(ctx), until)
	err = o.setCoordinatedLimit(fence, original, 0)
	stopFence()
	if err != nil {
		return err
	}
	original.FenceUntil = nil
	if err := writeReleaseRecord(c, coordinatedBackupIntentName, original); err != nil {
		return err
	}
	// The connection limit blocks new non-superuser connections. Existing
	// sessions must end before we inspect the database or the local files.
	const terminate = `SELECT coalesce(bool_and(pg_terminate_backend(pid, 5000)), true)
		FROM pg_stat_activity WHERE datname='ownsolid' AND backend_type='client backend' AND usename<>current_user`
	if answer, err := o.query(ctx, maintenanceDatabase, terminate); err != nil || answer != "t" {
		return errors.New("INSTALL_BACKUP_DRAIN_FAILED: application database connections did not stop")
	}
	if err := o.checkCoordinatedFence(ctx, original); err != nil {
		return err
	}
	if err := o.checkCoordinatedData(ctx); err != nil {
		return err
	}
	stamp, err := o.backupFileStamp()
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "step backup-fence: ok")
	published, result = o.backupWithPrefix(ctx, output, backupPrefix, func(partial string) error {
		return o.verifyCoordinatedCopy(ctx, original, partial, stamp)
	})
	return result
}

func coordinatedBackupHost(ctx context.Context, runner Runner) error {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return errors.New("INSTALL_BACKUP_HOST_REQUIRED: run as root on the local Linux Docker host")
	}
	return checkLocalDocker(ctx, runner)
}

func checkCoordinatedProfile(c Config) error {
	if c.PITR != nil || len(c.ControlSettings) != 0 || c.Telemetry.FleetHistoryControlDirectory != "" {
		return errBackupScope
	}
	l := c.Layout()
	compose, err := bundle.ReadProtectedConfiguration(l.ComposeFile)
	profile := fmt.Sprintf("%x", sha256.Sum256(compose))
	legacy := profile == legacyCoordinatedComposeSHA256
	if err != nil || profile != coordinatedComposeSHA256 && !legacy {
		return errBackupScope
	}
	env, err := bundle.ReadProtectedConfiguration(l.ComposeEnv)
	expected := c.renderComposeEnv()
	legacyEnv := strings.Replace(expected, "OWNSOLID_CONTROL_CA_FILE="+c.controlCAPath()+"\n", "", 1)
	if err != nil || !bytes.Equal(env, []byte(expected)) && !(legacy && bytes.Equal(env, []byte(legacyEnv))) {
		return errBackupScope
	}
	// The runtime user owns this file. Check the exact generated settings
	// instead of treating unrecognized configuration as a local profile.
	config, err := os.ReadFile(filepath.Join(l.Config, "control.yaml"))
	want, renderErr := c.renderControl(containerConfigDir+"/runtime-dsn", true)
	if err != nil || renderErr != nil || !bytes.Equal(config, want) {
		return errBackupScope
	}
	return nil
}

func (o *operation) admitCoordinatedBackup(ctx context.Context) (coordinatedBackupIntent, error) {
	var intent coordinatedBackupIntent
	if err := checkRestoreProject(ctx, o.config, o.runner); err != nil {
		return intent, err
	}
	if err := o.checkCoordinatedData(ctx); err != nil {
		return intent, err
	}
	database, err := o.coordinatedDB(ctx)
	if err != nil || database.Limit == 0 {
		return intent, errBackupScope
	}
	control, running, err := o.coordinatedControl(ctx)
	if err != nil {
		return intent, err
	}
	site, err := restoreSite(o.config)
	if err != nil {
		return intent, err
	}
	return coordinatedBackupIntent{Version: 1, Site: site, Database: database, ControlID: control, WasUp: running}, nil
}

func (o *operation) coordinatedControl(ctx context.Context) (string, bool, error) {
	raw, err := o.compose(ctx, "ps", "--all", "--no-trunc", "--format", "json")
	if err != nil {
		return "", false, errBackupFence
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	var id string
	var running bool
	for {
		var service struct {
			ID string
			ServiceStatus
		}
		err := decoder.Decode(&service)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", false, errBackupFence
		}
		switch service.Service {
		case "control":
			if id != "" || !containerID.MatchString(service.ID) || (service.State != "running" && service.State != "exited" && service.State != "created") {
				return "", false, errBackupFence
			}
			id, running = service.ID, service.State == "running"
		case "control-operator":
			if service.State != "exited" && service.State != "created" {
				return "", false, errBackupFence
			}
		case "postgres", "metrics", "logs":
		default:
			return "", false, errBackupFence
		}
	}
	if id == "" {
		return "", false, errBackupFence
	}
	return id, running, nil
}

func (o *operation) coordinatedDB(ctx context.Context) (coordinatedDB, error) {
	const observe = `SELECT json_build_object('cluster', system_identifier, 'oid', d.oid::bigint, 'limit', datconnlimit)
		FROM pg_database d CROSS JOIN pg_control_system()
		WHERE datname='ownsolid' AND datallowconn AND NOT pg_is_in_recovery()`
	var state coordinatedDB
	raw, err := o.query(ctx, maintenanceDatabase, observe)
	if err != nil || json.Unmarshal([]byte(raw), &state) != nil || state.Cluster == 0 || state.OID == 0 || state.Limit < -1 {
		return state, errBackupFence
	}
	return state, nil
}

func (o *operation) checkCoordinatedData(ctx context.Context) error {
	// Refuse all references to an uncovered store, even when its configuration
	// has since been removed. SQL is interpreted only by the local database.
	const check = `SELECT current_setting('shared_preload_libraries')='' AND
		NOT EXISTS (SELECT FROM pg_subscription WHERE subdbid=(SELECT oid FROM pg_database WHERE datname=current_database())) AND
		NOT EXISTS (SELECT FROM pg_prepared_xacts WHERE database=current_database()) AND
		NOT EXISTS (SELECT FROM public.artifact_objects) AND
		NOT EXISTS (SELECT FROM public.datasets) AND
		NOT EXISTS (SELECT FROM public.training_jobs) AND
		NOT EXISTS (SELECT FROM public.model_preparations)`
	if value, err := o.query(ctx, databaseName, check); err != nil || value != "t" {
		return errBackupScope
	}
	return nil
}

func (o *operation) stopCoordinatedControl(ctx context.Context, id string) error {
	step, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(o.config.StepSeconds)*time.Second)
	defer cancel()
	if _, err := o.runner.Run(step, "docker", "stop", id); err != nil {
		return errors.New("INSTALL_BACKUP_STOP_FAILED: control did not stop")
	}
	return nil
}

func (o *operation) setCoordinatedLimit(ctx context.Context, intent coordinatedBackupIntent, limit int) error {
	original := intent.Database
	expiry := ""
	if limit == 0 {
		expiry = fmt.Sprintf("IF clock_timestamp() >= '%s'::timestamptz THEN RAISE EXCEPTION 'backup fence request expired'; END IF;",
			intent.FenceUntil.Format(time.RFC3339Nano))
	}
	// Verify identity and the expected prior state in the same SQL transaction
	// as the change. Only the installation's maintenance user can bypass it.
	statement := fmt.Sprintf(`DO $fence$
	BEGIN
		IF NOT EXISTS (SELECT FROM pg_database d CROSS JOIN pg_control_system()
			WHERE datname='ownsolid' AND d.oid=%d AND system_identifier=%d AND datallowconn
			  AND datconnlimit IN (%d, 0) FOR UPDATE OF d) THEN
			RAISE EXCEPTION 'backup fence identity changed';
		END IF;
		%s
		ALTER DATABASE ownsolid CONNECTION LIMIT %d;
	END;
	$fence$`, original.OID, original.Cluster, original.Limit, expiry, limit)
	if _, err := o.query(ctx, maintenanceDatabase, statement); err != nil {
		return errors.Join(errBackupFence, err)
	}
	return nil
}

func (o *operation) checkCoordinatedFence(ctx context.Context, intent coordinatedBackupIntent) error {
	id, running, err := o.coordinatedControl(ctx)
	if err != nil || id != intent.ControlID || running {
		return errBackupFence
	}
	expected := intent.Database
	expected.Limit = 0
	database, err := o.coordinatedDB(ctx)
	if err != nil || database != expected {
		return errBackupFence
	}
	const sessions = `SELECT NOT EXISTS (SELECT FROM pg_stat_activity
		WHERE datname='ownsolid' AND backend_type='client backend' AND usename<>current_user)`
	if answer, err := o.query(ctx, maintenanceDatabase, sessions); err != nil || answer != "t" {
		return errBackupFence
	}
	return nil
}

type backupPathStamp struct {
	device, inode, links uint64
	uid, gid, mode       uint32
	size                 int64
	changed              unix.Timespec
}

func (o *operation) backupFileStamp() (map[string]backupPathStamp, error) {
	l := o.config.Layout()
	entries := map[string]backupPathStamp{}
	for _, root := range []string{l.Config, l.Operator, l.Secrets, o.modelObjects()} {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if path == o.modelObjects() && errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			if !d.IsDir() && d.Name() == operationLogName {
				return nil
			}
			var s unix.Stat_t
			if err := unix.Lstat(path, &s); err != nil {
				return err
			}
			kind := s.Mode & unix.S_IFMT
			if kind != unix.S_IFREG && kind != unix.S_IFDIR {
				return errors.New("the backup root contains an unsupported file type")
			}
			entries[path] = backupPathStamp{device: uint64(s.Dev), inode: s.Ino, links: uint64(s.Nlink),
				uid: s.Uid, gid: s.Gid, mode: uint32(s.Mode), size: s.Size, changed: s.Ctim}
			return nil
		})
		if err != nil {
			return nil, errors.New("INSTALL_BACKUP_FILES_CHANGED: cannot observe the local backup file inventory")
		}
	}
	return entries, nil
}

func (o *operation) verifyCoordinatedCopy(ctx context.Context, intent coordinatedBackupIntent, partial string, before map[string]backupPathStamp) error {
	manifest, err := readBackup(partial)
	if err != nil {
		return err
	}
	actual, err := o.fingerprint(ctx, databaseName)
	if err != nil || !slices.Equal(actual, manifest.Tables) {
		return errors.New("INSTALL_BACKUP_DATABASE_CHANGED: the live database differs from the checked dump")
	}
	if err := o.checkCoordinatedFence(ctx, intent); err != nil {
		return err
	}
	after, err := o.backupFileStamp()
	if err != nil || !maps.Equal(before, after) {
		return errors.New("INSTALL_BACKUP_FILES_CHANGED: protected files changed during coordinated backup")
	}
	fmt.Fprintln(o.out, "step backup-consistency: ok")
	return ctx.Err()
}

func readCoordinatedBackupIntent(c Config) (*coordinatedBackupIntent, error) {
	path := filepath.Join(c.InstallRoot, coordinatedBackupIntentName)
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	raw, err := bundle.ReadProtectedConfiguration(path)
	var intent coordinatedBackupIntent
	site, siteErr := restoreSite(c)
	if err != nil || siteErr != nil || wire.StrictJSON(raw, &intent) != nil || intent.Version != 1 ||
		intent.Site != site || !containerID.MatchString(intent.ControlID) || intent.Database.Cluster == 0 ||
		intent.Database.OID == 0 || intent.Database.Limit < -1 || intent.Database.Limit == 0 ||
		(intent.FenceUntil != nil && time.Until(*intent.FenceUntil) > coordinatedFenceWindow) {
		return nil, errors.New("INSTALL_BACKUP_RECOVERY_INVALID: preserve the record and use the original site configuration")
	}
	return &intent, nil
}

// RecoverCoordinatedBackup restores service after an interrupted backup.
func RecoverCoordinatedBackup(ctx context.Context, c Config, runner StreamRunner, out io.Writer) error {
	if err := coordinatedBackupHost(ctx, runner); err != nil {
		return err
	}
	unlock, err := lockRestoreOperation(c)
	if err != nil {
		return err
	}
	defer unlock()
	if err := restorePendingError(c); err != nil {
		return err
	}
	if coordinatedBackupPending(c) == nil {
		fmt.Fprintln(out, "BACKUP_NO_RECOVERY_PENDING")
		return nil
	}
	if err := checkBoundOperation(c); err != nil {
		return err
	}
	intent, err := readCoordinatedBackupIntent(c)
	if err != nil {
		return err
	}
	if intent == nil {
		fmt.Fprintln(out, "BACKUP_NO_RECOVERY_PENDING")
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.StepSeconds)*time.Second)
	defer cancel()
	o := newOperation(c, runner, out)
	if err := o.resumeCoordinatedBackup(ctx, *intent); err != nil {
		return errors.Join(errBackupPending, err)
	}
	o.record("coordinated-backup", "recovered", "original service state restored")
	fmt.Fprintln(out, "BACKUP_RECOVERED")
	return nil
}

func (o *operation) resumeCoordinatedBackup(ctx context.Context, intent coordinatedBackupIntent) error {
	id, running, err := o.coordinatedControl(ctx)
	if err != nil || id != intent.ControlID || (!intent.WasUp && running) {
		return errBackupFence
	}
	if intent.FenceUntil != nil {
		timer := time.NewTimer(time.Until(*intent.FenceUntil))
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	if running {
		// Synchronize with an earlier Docker stop even if its client disappeared.
		if err := o.stopCoordinatedControl(ctx, id); err != nil {
			return err
		}
	}
	if err := o.setCoordinatedLimit(ctx, intent, intent.Database.Limit); err != nil {
		return err
	}
	if intent.WasUp {
		if _, err := o.runner.Run(ctx, "docker", "start", id); err != nil {
			return errors.New("INSTALL_BACKUP_RESTART_FAILED: control did not start")
		}
		for {
			if err := o.health(ctx); err == nil {
				break
			}
			select {
			case <-ctx.Done():
				return errors.New("INSTALL_BACKUP_RESTART_FAILED: control did not become ready")
			case <-time.After(readinessPollInterval):
			}
		}
	}
	if err := os.Remove(filepath.Join(o.config.InstallRoot, coordinatedBackupIntentName)); err != nil {
		return err
	}
	return syncDirectory(o.config.InstallRoot)
}
