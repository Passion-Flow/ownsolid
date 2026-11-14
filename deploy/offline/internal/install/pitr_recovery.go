package install

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// pg_ctl can report a standby as started. Export only after SQL confirms promotion.
const pitrReplay = `umask 077
target=$1
wait=$2
shift 2
pg_combinebackup --output=/work/data "$@"
pg_verifybackup /work/data
test ! -e /work/data/standby.signal
: >/work/data/postgresql.auto.conf
printf 'local all all trust\n' >/work/hba.conf
cat >/work/replay.conf <<'CONFIG'
listen_addresses = ''
unix_socket_directories = '/work'
hba_file = '/work/hba.conf'
archive_mode = off
hot_standby = off
restore_command = 'cp /archive/%f %p'
recovery_target_timeline = 'current'
recovery_target_action = 'promote'
CONFIG
printf "recovery_target_time = '%s'\n" "$target" >>/work/replay.conf
touch /work/data/recovery.signal
trap 'if [ -f /work/data/postmaster.pid ]; then pg_ctl -D /work/data -m immediate -w stop; fi' EXIT
failed() {
    tail -n 12 /work/postgres.log >&2
    grep -E 'FATAL:|PANIC:' /work/postgres.log | tail -n 1 >&2
    exit 1
}
pg_ctl -D /work/data -o '-c config_file=/work/replay.conf' -l /work/postgres.log -t "$wait" -w start || failed
while :; do
    pg_ctl -D /work/data status >/dev/null 2>&1 || failed
    if state=$(PGCONNECT_TIMEOUT=2 psql -X -At -v ON_ERROR_STOP=1 -h /work -U ownsolid_migrator -d ownsolid -c 'SELECT pg_is_in_recovery()' 2>/dev/null) &&
        [ "$state" = f ]; then
        break
    fi
    [ "$wait" -gt 0 ] || failed
    sleep 1
    wait=$((wait-1))
done
pg_dump -h /work -U ownsolid_migrator -d ownsolid -Fc --file=/work/database.dump
`

// pitrRecovery binds a logical export to the site whose keys, schema and
// artifact store are needed to admit it through the normal restore path.
type pitrRecovery struct {
	ID      string `json:"id"`
	Base    string `json:"base"`
	Target  string `json:"target"`
	Owner   string `json:"owner"`
	Cluster string `json:"cluster"`
}

// PITRRecover replays a verified native chain to a UTC target in an isolated
// clone and publishes a logical backup. The live writer stays on its database.
func PITRRecover(ctx context.Context, c Config, runner StreamRunner, id, base, target string, out io.Writer) error {
	when, err := time.Parse(time.RFC3339Nano, target)
	if !pitrID.MatchString(id) || !pitrID.MatchString(base) || err != nil || !strings.HasSuffix(target, "Z") || when.Nanosecond()%1000 != 0 {
		return errors.New("INSTALL_PITR_TARGET_INVALID: specify valid IDs and a UTC RFC3339 time ending in Z with at most microsecond precision")
	}
	return withPITR(ctx, c, runner, out, func(ctx context.Context, j *pitrJob) error {
		// PostgreSQL's recovery GUC requires an explicit numeric UTC offset.
		const postgresTime = "2006-01-02 15:04:05.999999-07:00"
		request := pitrRecovery{id, base, when.UTC().Format(postgresTime), j.repo.owner.Token, j.source.cluster}
		if err := j.recoverTo(ctx, request); err != nil {
			j.record("pitr-replay", "failed", err.Error())
			return err
		}
		return nil
	})
}

func (j *pitrJob) recoverTo(ctx context.Context, request pitrRecovery) error {
	final := filepath.Join(j.repo.path, pitrRecovered, request.ID)
	if _, err := j.repo.root.Lstat(filepath.Join(pitrRecovered, request.ID)); !errors.Is(err, os.ErrNotExist) {
		if err := pitrDirectory(j.repo.root, filepath.Join(pitrRecovered, request.ID), 0, 0o700); err != nil {
			return err
		}
		prior, err := readBackup(final)
		if err != nil || prior.Recovery == nil || *prior.Recovery != request {
			return errors.New("INSTALL_PITR_ID_CONFLICT: select a new recovery ID or supply the original request")
		}
		fmt.Fprintf(j.out, "PITR_RECOVERY_ALREADY_COMPLETE path=%s\n", final)
		return nil
	}
	chain, err := j.baseChain(request.Base)
	if err != nil {
		return err
	}
	if err := j.verifyChain(ctx, chain); err != nil {
		return err
	}
	if err := pitrDirectory(j.repo.root, filepath.Join(pitrSpool, "wal", j.source.cluster), j.config.Runtime.PostgresUID, 0o700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Join(j.repo.path, pitrRecovered), ".recovery-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	replay := filepath.Join(stage, "replay")
	if err := os.Mkdir(replay, 0o700); err != nil {
		return err
	}
	if err := os.Chown(replay, j.config.Runtime.PostgresUID, -1); err != nil {
		return err
	}
	manifest := backupManifest{FormatVersion: backupFormatVersion, CreatedAt: j.now().UTC(),
		ControlImage: j.config.Images.Control, PostgresImage: j.config.Images.Postgres, Recovery: &request}
	tip := chain[len(chain)-1]
	for n, name := range keyFiles {
		file, err := copyPrivateFile(filepath.Join(j.config.InstallRoot, name), filepath.Join(stage, backupFilesName, name))
		if err != nil || file.SHA256 != tip.Keys[n] {
			return errors.New("INSTALL_PITR_KEYS_CHANGED: the current session or mail key differs from the selected base")
		}
		file.Path = name
		manifest.Files = append(manifest.Files, file)
	}

	for _, dir := range []string{filepath.Join(stage, backupFilesName, "config"), filepath.Join(stage, backupFilesName)} {
		if err := syncDirectory(dir); err != nil {
			return err
		}
	}

	mounts := []string{pitrBind(replay, "/work", false), pitrBind(j.repo.wal(j.source.cluster), "/archive", true)}
	args := []string{"sh", "-ceu", pitrReplay, "ownsolid-pitr", request.Target, strconv.Itoa(j.config.BackupSeconds)}
	for n, base := range chain {
		destination := fmt.Sprintf("/base-%d", n)
		mounts = append(mounts, pitrBind(filepath.Join(j.repo.base(base.ID), pitrData), destination, true))
		args = append(args, destination)
	}
	if err := j.tool(ctx, mounts, args...); err != nil {
		return fmt.Errorf("INSTALL_PITR_REPLAY_FAILED: verify the archived WAL and select a reachable target: %w", err)
	}
	dump := filepath.Join(stage, backupDatabaseName)
	if manifest.Database, err = copyPrivateFile(filepath.Join(replay, backupDatabaseName), dump); err != nil {
		return err
	}
	manifest.Database.Path = backupDatabaseName
	if err := os.RemoveAll(replay); err != nil {
		return err
	}
	random, err := randomBytes(12)
	if err != nil {
		return err
	}
	manifest.SchemaVersion, manifest.Tables, err = j.verifyDump(ctx, dump, fmt.Sprintf("ownsolid_verify_pitr_%x", random))
	if err != nil {
		return fmt.Errorf("INSTALL_PITR_LOGICAL_VERIFY_FAILED: %w", err)
	}
	live, err := j.schemaVersion(ctx, databaseName)
	if err != nil || live != manifest.SchemaVersion {
		return errors.New("INSTALL_PITR_SCHEMA_CHANGED: choose a target on the installed schema")
	}
	if err := writeManifest(filepath.Join(stage, backupManifestName), manifest); err != nil {
		return err
	}
	if err := publishPITRDirectory(stage, final); err != nil {
		return err
	}
	j.record("pitr-replay", "complete", request.ID)
	fmt.Fprintf(j.out, "PITR_RECOVERY_COMPLETE path=%s\n", final)
	return nil
}

const pitrActivationSetting = "ownsolid.pitr_activation"

func (m backupManifest) pitrActivation() string {
	if m.Recovery == nil {
		return ""
	}
	return m.Recovery.ID + "/" + m.Database.SHA256
}

func validPITRActivation(value string) bool {
	id, digest, found := strings.Cut(value, "/")
	return found && pitrID.MatchString(id) && pitrDigest.MatchString(digest)
}

func (o *operation) admitPITRRestore(ctx context.Context, m backupManifest) (bool, error) {
	r := m.Recovery
	activation := m.pitrActivation()
	if !validPITRActivation(activation) {
		return false, errors.New("INSTALL_PITR_ACTIVATION_INVALID: check the recovery artifact")
	}
	owner, err := readPITROwner(o.config, false)
	if err != nil || r.Owner != owner.Token {
		return false, errors.New("INSTALL_PITR_RESTORE_FOREIGN: activate this recovery only on the installation that created it")
	}
	raw, err := o.query(ctx, maintenanceDatabase, "SELECT system_identifier::text FROM pg_catalog.pg_control_system()")
	cluster, parseErr := strconv.ParseInt(raw, 10, 64)
	if err != nil || parseErr != nil || r.Cluster != strconv.FormatUint(uint64(cluster), 10) {
		return false, errors.New("INSTALL_PITR_CLUSTER_CHANGED: activate this recovery only on the original PostgreSQL cluster")
	}
	version, err := o.schemaVersion(ctx, databaseName)
	if err != nil || version != m.SchemaVersion {
		return false, errors.New("INSTALL_PITR_SCHEMA_CHANGED: this recovery must match the installed schema")
	}
	const marker = `SELECT split_part(setting, '=', 2)
		FROM pg_catalog.pg_db_role_setting CROSS JOIN LATERAL unnest(setconfig) AS settings(setting)
		WHERE setrole = 0 AND setdatabase = (SELECT oid FROM pg_catalog.pg_database WHERE datname = current_database())
		  AND split_part(setting, '=', 1) = '` + pitrActivationSetting + `'`
	active, err := o.query(ctx, databaseName, marker)
	if err != nil || (active != "" && !validPITRActivation(active)) {
		return false, errors.New("INSTALL_PITR_ACTIVATION_INVALID: cannot verify the active recovery marker")
	}
	if active == activation {
		adopted, err := o.hasRestoreAdoption(ctx)
		if err != nil || !adopted {
			return false, errors.New("INSTALL_PITR_ACTIVATION_INVALID: cannot verify the active recovery adoption")
		}
		return true, nil
	}
	if strings.HasPrefix(active, r.ID+"/") {
		return false, errors.New("INSTALL_PITR_ID_CONFLICT: the active recovery ID has a different dump")
	}
	return false, nil
}
