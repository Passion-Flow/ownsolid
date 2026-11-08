package install

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"go.yaml.in/yaml/v3"
)

const (
	pitrComposeName = "compose-pitr.yaml"
	pitrOwnerName   = "pitr-owner.json"
	pitrMount       = "/ownsolid-pitr"
	pitrSpool       = "spool"
	pitrBases       = "bases"
	pitrRecovered   = "recovered"
	pitrRecordName  = "base.json"
	pitrData        = "data"
	pitrManifest    = "backup_manifest"
)

var (
	pitrID          = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)
	pitrToken       = regexp.MustCompile(`^[a-f0-9]{32}$`)
	pitrContainerID = regexp.MustCompile(`^[a-f0-9]{12,64}$`)
	pitrImageID     = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

// PITRConfig enables PostgreSQL continuous archiving into a private local repository.
type PITRConfig struct {
	Directory        string `yaml:"directory"`
	ArchiveSeconds   int    `yaml:"archive_seconds"`
	SummaryKeepHours int    `yaml:"summary_keep_hours"`
}

func (p PITRConfig) validate() error {
	if !canonicalPath(p.Directory) || strings.ContainsAny(p.Directory, ",\"$\n\r") {
		return invalid("pitr.directory", "must be a clean absolute path without commas, quotes or dollar signs")
	}
	if p.ArchiveSeconds < 0 || p.ArchiveSeconds > 3600 || p.SummaryKeepHours < 0 || p.SummaryKeepHours > 8760 {
		return invalid("pitr", "archive_seconds must be 0 to 3600 and summary_keep_hours 0 to 8760; zero uses the default")
	}
	return nil
}

// PostgreSQL invokes this from PGDATA. The system identifier separates the
// streams even if a new cluster is initialized under the same installation.
// Keep the argument on one line so the image's temporary initdb server can
// round-trip it through Bash printf %q and the POSIX shell used by pg_ctl.
const pitrArchiveCommand = `set -eu; export LC_ALL=C; ` +
	`cluster=$(pg_controldata . | sed -n 's/^Database system identifier: *//p'); ` +
	`case "$cluster" in ''|*[!0-9]*) exit 1;; esac; ` +
	`dir=/ownsolid-pitr/wal/$cluster; umask 077; mkdir -p "$dir"; file="$dir/%f"; ` +
	`if [ -e "$file" ]; then cmp -s "%p" "$file"; sync "$file" "$dir" /ownsolid-pitr/wal; exit; fi; ` +
	`temp=$(mktemp "$dir/.segment.XXXXXXXX"); trap 'unlink "$temp"' EXIT; cp "%p" "$temp"; sync "$temp"; ` +
	`if ! ln "$temp" "$file"; then cmp -s "%p" "$file"; fi; sync "$dir" /ownsolid-pitr/wal`

type pitrOwner struct {
	Version     int    `json:"version"`
	Token       string `json:"token"`
	Project     string `json:"project"`
	InstallRoot string `json:"install_root"`
}

type pitrRepo struct {
	root  *os.Root
	lock  *os.File
	path  string
	owner pitrOwner
}

func (r *pitrRepo) close() { r.lock.Close(); r.root.Close() }

func (r *pitrRepo) wal(cluster string) string {
	return filepath.Join(r.path, pitrSpool, "wal", cluster)
}

func (r *pitrRepo) base(id string) string {
	return filepath.Join(r.path, pitrBases, id)
}

func readPITROwner(c Config, initialize bool) (pitrOwner, error) {
	path := filepath.Join(c.InstallRoot, pitrOwnerName)
	var owner pitrOwner
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) && initialize {
		token, err := randomBytes(16)
		if err != nil {
			return owner, err
		}
		owner = pitrOwner{1, hex.EncodeToString(token), c.Project, c.InstallRoot}
		return owner, writeReleaseRecord(c, pitrOwnerName, owner)
	}
	raw, err := bundle.ReadProtectedConfiguration(path)
	if err != nil || wire.StrictJSON(raw, &owner) != nil || owner.Version != 1 ||
		!pitrToken.MatchString(owner.Token) || owner.Project != c.Project || owner.InstallRoot != c.InstallRoot {
		return owner, errors.New("INSTALL_PITR_OWNER_INVALID: keep the installed PITR owner record and use its original site configuration")
	}
	return owner, nil
}

func openPITRRepo(c Config, initialize bool) (*pitrRepo, error) {
	path := c.PITR.Directory
	if err := admitBackupOutput(c, path); err != nil {
		return nil, err
	}
	// The configured path is also used by the Docker daemon. Require ancestors
	// whose entries cannot be replaced by another account.
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Sys().(*syscall.Stat_t).Uid != 0 ||
			(info.Mode().Perm()&0o022 != 0 && info.Mode()&os.ModeSticky == 0) {
			return nil, errors.New("INSTALL_PITR_REPOSITORY_INVALID: use a root-owned private directory with protected ancestors")
		}
		if dir == "/" {
			break
		}
	}
	owner, err := readPITROwner(c, initialize)
	if err != nil {
		return nil, err
	}
	root, lock, err := openScheduleDirectory(c, path)
	if err != nil {
		return nil, fmt.Errorf("INSTALL_PITR_REPOSITORY_UNAVAILABLE: %w", err)
	}
	r := &pitrRepo{root: root, lock: lock, path: path, owner: owner}
	if err := r.checkOwner(c, initialize); err != nil {
		r.close()
		return nil, err
	}
	return r, nil
}

func (r *pitrRepo) checkOwner(c Config, initialize bool) error {
	raw, err := json.Marshal(r.owner)
	if err != nil {
		return err
	}
	same, err := scheduleOwnerMatches(r.root, pitrOwnerName, raw)
	if errors.Is(err, os.ErrNotExist) && initialize {
		if entries, err := r.lock.Readdirnames(1); len(entries) != 0 || !errors.Is(err, io.EOF) {
			return errors.New("INSTALL_PITR_REPOSITORY_OCCUPIED: select a new empty repository")
		}
		if err := writeFile(filepath.Join(r.path, pitrOwnerName), raw, c, false); err != nil {
			return err
		}
		if err := r.lock.Sync(); err != nil {
			return err
		}
		return nil
	}
	if err != nil || !same {
		return errors.New("INSTALL_PITR_REPOSITORY_FOREIGN: the repository belongs to another installation")
	}
	return nil
}

func materializePITR(c Config) error {
	r, err := openPITRRepo(c, true)
	if err != nil {
		return err
	}
	defer r.close()
	for _, directory := range []struct {
		name string
		mode os.FileMode
		uid  int
	}{
		{pitrBases, 0o700, 0}, {pitrRecovered, 0o700, 0}, {pitrSpool, 0o755, 0},
		{filepath.Join(pitrSpool, "incoming"), 0o700, c.Runtime.PostgresUID},
		{filepath.Join(pitrSpool, "wal"), 0o700, c.Runtime.PostgresUID},
	} {
		if err := r.ensureDirectory(directory.name, directory.uid, directory.mode); err != nil {
			return err
		}
	}
	raw, err := renderPITRCompose(c)
	if err != nil {
		return err
	}
	if err := writeFile(filepath.Join(c.InstallRoot, pitrComposeName), raw, c, false); err != nil {
		return err
	}
	return r.lock.Sync()
}

func (r *pitrRepo) ensureDirectory(name string, uid int, mode os.FileMode) error {
	if _, err := r.root.Lstat(name); !errors.Is(err, os.ErrNotExist) {
		return pitrDirectory(r.root, name, uid, mode)
	}
	path := filepath.Join(r.path, name)
	stage, err := os.MkdirTemp(filepath.Dir(path), ".prepare-")
	if err != nil {
		return err
	}
	defer os.Remove(stage)
	if err := os.Chown(stage, uid, -1); err != nil {
		return err
	}
	if err := os.Chmod(stage, mode); err != nil {
		return err
	}
	return publishPITRDirectory(stage, path)
}

func pitrDirectory(root *os.Root, name string, uid int, mode os.FileMode) error {
	info, err := root.Lstat(name)
	if err != nil || !info.IsDir() || info.Mode().Perm() != mode || int(info.Sys().(*syscall.Stat_t).Uid) != uid {
		return fmt.Errorf("INSTALL_PITR_DIRECTORY_INVALID: check repository directory %s and its owner", name)
	}
	return nil
}

func renderPITRCompose(c Config) ([]byte, error) {
	archive, summaries := c.PITR.ArchiveSeconds, c.PITR.SummaryKeepHours
	if archive == 0 {
		archive = 60
	}
	if summaries == 0 {
		summaries = 240
	}
	return yaml.Marshal(map[string]any{"services": map[string]any{"postgres": map[string]any{
		"command": []string{"postgres", "-c", fmt.Sprintf("max_connections=%d", c.Resources.PostgresMaxConnections),
			"-c", "archive_mode=on", "-c", "wal_level=replica", "-c", "summarize_wal=on",
			"-c", fmt.Sprintf("archive_timeout=%ds", archive),
			"-c", fmt.Sprintf("wal_summary_keep_time=%dh", summaries),
			"-c", "archive_command=" + strings.ReplaceAll(pitrArchiveCommand, "$", "$$")},
		"volumes": []any{map[string]any{"type": "bind", "source": filepath.Join(c.PITR.Directory, pitrSpool),
			"target": pitrMount, "bind": map[string]bool{"create_host_path": false}}},
	}}})
}

type pitrSource struct {
	cluster string
	image   string
}

func (o *operation) pitrSource(ctx context.Context, r *pitrRepo) (pitrSource, error) {
	var result pitrSource
	id, err := o.compose(ctx, "ps", "--quiet", "postgres")
	if err != nil || !pitrContainerID.MatchString(strings.TrimSpace(id)) {
		return result, errors.New("INSTALL_PITR_POSTGRES_UNAVAILABLE: apply the PITR configuration and start PostgreSQL")
	}
	const format = `{"image":{{json .Image}},"mounts":{{json .Mounts}}}`
	raw, err := o.runner.Run(ctx, "docker", "inspect", "--type", "container", "--format", format, strings.TrimSpace(id))
	var info struct {
		Image  string
		Mounts []struct {
			Type, Source, Destination string
			RW                        bool
		}
	}
	if err != nil || json.Unmarshal([]byte(raw), &info) != nil || !pitrImageID.MatchString(info.Image) {
		return result, errors.New("INSTALL_PITR_POSTGRES_UNAVAILABLE: cannot identify the running PostgreSQL image")
	}
	mounted := false
	for _, mount := range info.Mounts {
		if mount.Destination == pitrMount && mount.Type == "bind" && mount.RW && mount.Source == filepath.Join(r.path, pitrSpool) {
			mounted = true
		}
	}
	if !mounted {
		return result, errors.New("INSTALL_PITR_POSTGRES_UNAVAILABLE: apply the configuration to mount this repository")
	}
	const settings = `SELECT json_build_object('cluster', system_identifier::text,
		'version', current_setting('server_version_num')::integer, 'archive', current_setting('archive_mode'),
		'summary', current_setting('summarize_wal'), 'command', current_setting('archive_command'),
		'recovery', pg_is_in_recovery(), 'tablespaces', (SELECT count(*) FROM pg_tablespace WHERE spcname NOT IN ('pg_default','pg_global')))
		FROM pg_catalog.pg_control_system()`
	raw, err = o.query(ctx, maintenanceDatabase, settings)
	var db struct {
		Cluster, Archive, Summary, Command string
		Version, Tablespaces               int
		Recovery                           bool
	}
	if err != nil || json.Unmarshal([]byte(raw), &db) != nil || db.Version < 180000 || db.Version >= 190000 ||
		db.Archive != "on" || db.Summary != "on" || db.Command != pitrArchiveCommand || db.Recovery || db.Tablespaces != 0 {
		return result, errors.New("INSTALL_PITR_POSTGRES_INVALID: require the configured PostgreSQL 18 primary with archiving, WAL summaries and default tablespaces")
	}
	cluster, err := strconv.ParseInt(db.Cluster, 10, 64)
	if err != nil || cluster == 0 {
		return result, errors.New("INSTALL_PITR_POSTGRES_INVALID: cannot read the cluster system identifier")
	}
	return pitrSource{strconv.FormatUint(uint64(cluster), 10), info.Image}, nil
}
