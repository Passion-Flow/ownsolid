package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

const pitrMaxChain = 64

var pitrDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

type pitrBase struct {
	Version  int      `json:"version"`
	ID       string   `json:"id"`
	Owner    string   `json:"owner"`
	Cluster  string   `json:"cluster"`
	Image    string   `json:"image"`
	Parent   string   `json:"parent,omitempty"`
	Manifest string   `json:"manifest_sha256"`
	Timeline uint32   `json:"timeline"`
	Start    uint64   `json:"start_lsn"`
	End      uint64   `json:"end_lsn"`
	Keys     []string `json:"key_sha256"`
}

type pitrJob struct {
	*operation
	repo   *pitrRepo
	source pitrSource
}

func withPITR(ctx context.Context, c Config, runner StreamRunner, out io.Writer, do func(context.Context, *pitrJob) error) error {
	if os.Geteuid() != 0 {
		return errors.New("INSTALL_ROOT_REQUIRED: run PITR on the Linux installation host as root")
	}
	if c.PITR == nil {
		return errors.New("INSTALL_PITR_REQUIRED: configure pitr and apply the installation first")
	}
	if err := c.PITR.validate(); err != nil {
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
	if err := checkInstalledProject(c); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.BackupSeconds)*time.Second)
	defer cancel()
	if err := checkLocalDocker(ctx, runner); err != nil {
		return err
	}
	repo, err := openPITRRepo(c, false)
	if err != nil {
		return err
	}
	defer repo.close()
	for _, name := range []string{pitrBases, pitrRecovered} {
		if err := pitrDirectory(repo.root, name, 0, 0o700); err != nil {
			return err
		}
	}
	o := newOperation(c, runner, out)
	source, err := o.pitrSource(ctx, repo)
	if err != nil {
		return err
	}
	return do(ctx, &pitrJob{o, repo, source})
}

// PITRBackup publishes a verified PostgreSQL base backup. An existing ID
// returns the verified backup only if it belongs to the same request.
func PITRBackup(ctx context.Context, c Config, runner StreamRunner, id, parent string, out io.Writer) error {
	if !pitrID.MatchString(id) || (parent != "" && (!pitrID.MatchString(parent) || parent == id)) {
		return errors.New("INSTALL_PITR_ID_INVALID: use distinct lowercase backup IDs of at most 48 characters")
	}
	return withPITR(ctx, c, runner, out, func(ctx context.Context, j *pitrJob) error {
		if err := j.backupBase(ctx, id, parent); err != nil {
			j.record("backup", "failed", err.Error())
			return err
		}
		return nil
	})
}

func (j *pitrJob) backupBase(ctx context.Context, id, parent string) error {
	final := j.repo.base(id)
	if _, err := j.repo.root.Lstat(filepath.Join(pitrBases, id)); !errors.Is(err, os.ErrNotExist) {
		chain, err := j.baseChain(id)
		if err != nil {
			return err
		}
		if chain[len(chain)-1].Parent != parent {
			return errors.New("INSTALL_PITR_ID_CONFLICT: this backup ID was created with a different parent")
		}
		if err := j.verifyChain(ctx, chain); err != nil {
			return err
		}
		fmt.Fprintf(j.out, "PITR_BACKUP_ALREADY_COMPLETE path=%s\n", final)
		return nil
	}
	var chain []pitrBase
	if parent != "" {
		var err error
		chain, err = j.baseChain(parent)
		if err != nil {
			return err
		}
		if len(chain) == pitrMaxChain {
			return errors.New("INSTALL_PITR_CHAIN_LIMIT: take a new full backup")
		}
		if err := j.verifyChain(ctx, chain); err != nil {
			return err
		}
	}
	incoming := filepath.Join(j.repo.path, pitrSpool, "incoming")
	if err := pitrDirectory(j.repo.root, filepath.Join(pitrSpool, "incoming"), j.config.Runtime.PostgresUID, 0o700); err != nil {
		return err
	}
	work, err := os.MkdirTemp(incoming, "base-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	if err := os.Chown(work, j.config.Runtime.PostgresUID, -1); err != nil {
		return err
	}
	containerWork := path.Join(pitrMount, "incoming", filepath.Base(work))
	data := filepath.Join(work, pitrData)
	args := []string{"pg_basebackup", "-U", migratorRole, "--no-password", "--checkpoint=spread", "--wal-method=stream",
		"--manifest-checksums=SHA256", "--pgdata=" + path.Join(containerWork, pitrData)}
	if parent != "" {
		parentManifest := filepath.Join(work, "parent.manifest")
		if _, err := copyPrivateFile(filepath.Join(j.repo.base(parent), pitrData, pitrManifest), parentManifest); err != nil {
			return err
		}
		if err := os.Chown(parentManifest, j.config.Runtime.PostgresUID, -1); err != nil {
			return err
		}
		args = append(args, "--incremental="+path.Join(containerWork, "parent.manifest"))
	}
	if err := j.sourceCommand(ctx, args...); err != nil {
		return fmt.Errorf("INSTALL_PITR_BASE_FAILED: check PostgreSQL and take a new full base if the parent WAL summaries are gone: %w", err)
	}
	if err := j.sourceCommand(ctx, "pg_verifybackup", path.Join(containerWork, pitrData)); err != nil {
		return fmt.Errorf("INSTALL_PITR_VERIFY_FAILED: %w", err)
	}
	record, err := measurePITRBase(data)
	if err != nil {
		return err
	}
	if record.Cluster != j.source.cluster {
		return errors.New("INSTALL_PITR_CLUSTER_CHANGED: PostgreSQL changed during the base backup")
	}
	if len(chain) > 0 {
		previous := chain[len(chain)-1]
		if previous.Timeline != record.Timeline || previous.End > record.Start {
			return errors.New("INSTALL_PITR_PARENT_INVALID: take a full backup on the current timeline")
		}
	}
	record.Version, record.ID, record.Owner = 1, id, j.repo.owner.Token
	record.Parent, record.Image = parent, j.source.image
	for _, name := range keyFiles {
		digest, err := pitrFileDigest(filepath.Join(j.config.InstallRoot, name))
		if err != nil {
			return err
		}
		record.Keys = append(record.Keys, digest)
	}

	stage, err := os.MkdirTemp(filepath.Join(j.repo.path, pitrBases), ".base-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := os.Rename(data, filepath.Join(stage, pitrData)); err != nil {
		return err
	}
	if err := writePITRRecord(j.config, filepath.Join(stage, pitrRecordName), record); err != nil {
		return err
	}
	if err := publishPITRDirectory(stage, final); err != nil {
		return err
	}
	j.record("backup", "complete", "PITR base "+id)
	fmt.Fprintf(j.out, "PITR_BACKUP_COMPLETE path=%s\n", final)
	return nil
}

func (j *pitrJob) sourceCommand(ctx context.Context, args ...string) error {
	prefix := []string{"exec", "--no-TTY", "--user", strconv.Itoa(j.config.Runtime.PostgresUID), "postgres",
		"timeout", "--kill-after=5s", fmt.Sprintf("%ds", j.config.BackupSeconds)}
	raw, err := j.compose(ctx, append(prefix, args...)...)
	if err != nil {
		return errors.New(lastLines(raw))
	}
	return nil
}

func (j *pitrJob) tool(ctx context.Context, mounts []string, args ...string) error {
	run := []string{"run", "--rm", "--pull", "never", "--network", "none", "--user", strconv.Itoa(j.config.Runtime.PostgresUID),
		"--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
		"--memory", j.config.Resources.PostgresMemory, "--cpus", j.config.Resources.PostgresCPUs, "--pids-limit", "128"}
	for _, mount := range mounts {
		run = append(run, "--mount", mount)
	}
	run = append(run, "--entrypoint", "timeout", j.source.image, "--kill-after=5s", fmt.Sprintf("%ds", j.config.BackupSeconds))
	raw, err := j.runner.Run(ctx, "docker", append(run, args...)...)
	if err != nil {
		return errors.New(lastLines(raw))
	}
	return nil
}

func pitrBind(source, target string, readOnly bool) string {
	mount := "type=bind,source=" + source + ",target=" + target
	if readOnly {
		mount += ",readonly"
	}
	return mount
}

func (j *pitrJob) readBase(id string) (pitrBase, error) {
	var b pitrBase
	name := filepath.Join(pitrBases, id)
	if err := pitrDirectory(j.repo.root, name, 0, 0o700); err != nil {
		return b, err
	}
	raw, err := bundle.ReadProtectedConfiguration(filepath.Join(j.repo.base(id), pitrRecordName))
	if err != nil || wire.StrictJSON(raw, &b) != nil || b.Version != 1 || b.ID != id ||
		b.Owner != j.repo.owner.Token || b.Cluster != j.source.cluster || b.Image != j.source.image ||
		(b.Parent != "" && !pitrID.MatchString(b.Parent)) || !pitrDigest.MatchString(b.Manifest) || b.Start >= b.End ||
		b.Timeline == 0 || len(b.Keys) != len(keyFiles) {
		return b, fmt.Errorf("INSTALL_PITR_BASE_INVALID: check the owner, cluster, image and metadata of base %s", id)
	}
	for _, hash := range b.Keys {
		if !pitrDigest.MatchString(hash) {
			return b, fmt.Errorf("INSTALL_PITR_BASE_INVALID: base %s has invalid key fingerprints", id)
		}
	}
	if err := pitrDirectory(j.repo.root, filepath.Join(name, pitrData), j.config.Runtime.PostgresUID, 0o700); err != nil {
		return b, err
	}
	measured, err := measurePITRBase(filepath.Join(j.repo.base(id), pitrData))
	if err != nil || measured.Manifest != b.Manifest || measured.Cluster != b.Cluster ||
		measured.Start != b.Start || measured.End != b.End || measured.Timeline != b.Timeline {
		return b, fmt.Errorf("INSTALL_PITR_BASE_INVALID: base %s does not match its native backup manifest", id)
	}
	return b, nil
}

// baseChain returns dependencies in PostgreSQL combine order.
func (j *pitrJob) baseChain(id string) ([]pitrBase, error) {
	var chain []pitrBase
	seen := make(map[string]bool)
	for id != "" {
		if seen[id] || len(chain) == pitrMaxChain {
			return nil, errors.New("INSTALL_PITR_CHAIN_INVALID: base dependency cycle or chain limit exceeded")
		}
		b, err := j.readBase(id)
		if err != nil {
			return nil, err
		}
		if len(chain) > 0 && (b.Timeline != chain[0].Timeline || b.End > chain[len(chain)-1].Start) {
			return nil, errors.New("INSTALL_PITR_CHAIN_INVALID: base dependencies do not share an ordered timeline")
		}
		chain = append(chain, b)
		seen[id] = true
		id = b.Parent
	}
	slices.Reverse(chain)
	return chain, nil
}

func (j *pitrJob) verifyChain(ctx context.Context, chain []pitrBase) error {
	for _, base := range chain {
		mount := pitrBind(filepath.Join(j.repo.base(base.ID), pitrData), "/base", true)
		if err := j.tool(ctx, []string{mount}, "pg_verifybackup", "/base"); err != nil {
			return fmt.Errorf("INSTALL_PITR_VERIFY_FAILED: base %s: %w", base.ID, err)
		}
	}
	return nil
}

func measurePITRBase(data string) (pitrBase, error) {
	var result pitrBase
	f, err := os.OpenFile(filepath.Join(data, pitrManifest), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return result, err
	}
	defer f.Close()
	const maxManifest = 128 << 20
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxManifest {
		return result, errors.New("INSTALL_PITR_MANIFEST_INVALID: expected a bounded native PostgreSQL manifest")
	}
	var m struct {
		Cluster uint64 `json:"System-Identifier"`
		Ranges  []struct {
			Timeline uint32
			Start    string `json:"Start-LSN"`
			End      string `json:"End-LSN"`
		} `json:"WAL-Ranges"`
	}
	hash := sha256.New()
	decoder := json.NewDecoder(io.TeeReader(io.LimitReader(f, maxManifest+1), hash))
	if err := decoder.Decode(&m); err != nil || m.Cluster == 0 || len(m.Ranges) != 1 || !errors.Is(decoder.Decode(new(any)), io.EOF) {
		return result, errors.New("INSTALL_PITR_MANIFEST_INVALID: require a native backup from one PostgreSQL timeline")
	}
	wal := m.Ranges[0]
	start, startErr := pitrLSN(wal.Start)
	end, endErr := pitrLSN(wal.End)
	if startErr != nil || endErr != nil || start >= end || wal.Timeline == 0 {
		return result, errors.New("INSTALL_PITR_MANIFEST_INVALID: invalid WAL range")
	}
	return pitrBase{Cluster: strconv.FormatUint(m.Cluster, 10), Manifest: hex.EncodeToString(hash.Sum(nil)), Timeline: wal.Timeline, Start: start, End: end}, nil
}

func pitrLSN(s string) (uint64, error) {
	left, right, found := strings.Cut(s, "/")
	high, e1 := strconv.ParseUint(left, 16, 32)
	low, e2 := strconv.ParseUint(right, 16, 32)
	if !found || e1 != nil || e2 != nil {
		return 0, errors.New("invalid PostgreSQL LSN")
	}
	return high<<32 | low, nil
}

func pitrFileDigest(name string) (string, error) {
	f, err := os.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("INSTALL_PITR_FILE_INVALID: expected a regular file")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func writePITRRecord(c Config, name string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writeFile(name, raw, c, false)
}

func publishPITRDirectory(stage, final string) error {
	if err := syncDirectory(stage); err != nil {
		return err
	}
	if err := os.Rename(stage, final); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(final))
}
