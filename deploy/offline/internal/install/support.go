package install

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type supportReport struct {
	FormatVersion  int                  `json:"format_version"`
	CollectedAt    time.Time            `json:"collected_at"`
	Host           supportHost          `json:"host"`
	Topology       string               `json:"topology"`
	Configuration  supportConfiguration `json:"configuration"`
	Operations     []supportOperation   `json:"recent_operations"`
	Services       []supportService     `json:"services"`
	SchemaVersion  *int                 `json:"schema_version,omitempty"`
	RestorePhase   string               `json:"pending_restore_phase,omitempty"`
	BackupRecovery string               `json:"coordinated_backup_recovery,omitempty"`
	Issues         []string             `json:"collection_issues"`
}

// The deployment configuration contains secret references and site data. Only
// the fields listed here may be written to a support archive.
type supportConfiguration struct {
	TLSSource              string                             `json:"tls_source"`
	SMTPTLSMode            string                             `json:"smtp_tls_mode"`
	StepSeconds            int                                `json:"step_seconds"`
	BackupSeconds          int                                `json:"backup_seconds"`
	PreflightEnabled       bool                               `json:"preflight_enabled"`
	PostgresMaxConnections int                                `json:"postgres_max_connections"`
	MetricsRetention       string                             `json:"metrics_retention"`
	LogsRetention          string                             `json:"logs_retention"`
	Resources              map[string]supportServiceResources `json:"resources"`
}

type supportServiceResources struct {
	Memory string `json:"memory"`
	CPUs   string `json:"cpus"`
}

func supportConfig(c Config) supportConfiguration {
	r := c.Resources
	return supportConfiguration{
		TLSSource: c.TLS.Source, SMTPTLSMode: c.SMTP.TLSMode,
		StepSeconds: c.StepSeconds, BackupSeconds: c.BackupSeconds, PreflightEnabled: c.Preflight != nil,
		PostgresMaxConnections: r.PostgresMaxConnections,
		MetricsRetention:       c.Telemetry.MetricsRetention, LogsRetention: c.Telemetry.LogsRetention,
		Resources: map[string]supportServiceResources{
			"control":  {Memory: r.ControlMemory, CPUs: r.ControlCPUs},
			"postgres": {Memory: r.PostgresMemory, CPUs: r.PostgresCPUs},
			"metrics":  {Memory: r.MetricsMemory, CPUs: r.MetricsCPUs},
			"logs":     {Memory: r.LogsMemory, CPUs: r.LogsCPUs},
		},
	}
}

type supportHost struct {
	OS            string  `json:"os"`
	Architecture  string  `json:"architecture"`
	Kernel        string  `json:"kernel,omitempty"`
	DataFreeBytes *uint64 `json:"data_free_bytes,omitempty"`
	Docker        bool    `json:"docker_available"`
}

type supportService struct {
	Service string `json:"service"`
	State   string `json:"state"`
	Health  string `json:"health,omitempty"`
}

// SupportBundle saves a redacted snapshot on the installation host, even when control is down.
func SupportBundle(ctx context.Context, c Config, runner Runner, output string, out io.Writer) error {
	return publishSupportBundle(ctx, output, time.Duration(c.StepSeconds)*time.Second, out,
		func(admission context.Context) (*supportAccess, error) {
			return installationSupportAccess(admission, c)
		},
		func(collection context.Context, access *supportAccess) (any, int) {
			report := collectSupport(collection, c, runner, access.priorSize)
			if errors.Is(collection.Err(), context.DeadlineExceeded) {
				report.Issues = append(report.Issues, "COLLECTION_TIMEOUT")
			}
			return report, len(report.Issues)
		})
}

func publishSupportBundle(ctx context.Context, output string, timeout time.Duration, out io.Writer,
	admit func(context.Context) (*supportAccess, error), collect func(context.Context, *supportAccess) (any, int)) (result error) {
	if !canonicalPath(output) || !strings.HasSuffix(output, ".tar.gz") {
		return errors.New("INSTALL_SUPPORT_OUTPUT_INVALID: supply an absolute path ending in .tar.gz")
	}
	parent, err := os.OpenRoot(filepath.Dir(output))
	if err != nil {
		return errors.New("INSTALL_SUPPORT_OUTPUT_INVALID: create the output directory and check its permissions")
	}
	defer parent.Close()
	info, err := parent.Stat(".")
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
		return errors.New("INSTALL_SUPPORT_OUTPUT_INVALID: use an output directory that only its owner can write")
	}
	name := filepath.Base(output)
	if _, err := parent.Lstat(name); !errors.Is(err, os.ErrNotExist) {
		return errors.New("INSTALL_SUPPORT_OUTPUT_EXISTS: use an unused output path")
	}
	collection, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	access, err := admit(collection)
	if err != nil {
		return err
	}
	defer access.close()
	terminalAttempted := false
	defer func() {
		if result == nil || terminalAttempted {
			return
		}
		failure := "INSTALL_SUPPORT_COLLECTION_FAILED"
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			failure = "INSTALL_SUPPORT_CANCELLED"
		}
		completion, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
		defer cancel()
		result = errors.Join(result, access.append(completion, "failed", supportArchive{}, failure))
	}()
	report, issues := collect(collection, access)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("INSTALL_SUPPORT_CANCELLED: %w", err)
	}
	archive, err := writeSupport(parent, name, report)
	if err != nil {
		return errors.Join(err, removeSupportArchive(parent, name, archive))
	}
	// Never retry an uncertain terminal append or release a successful payload.
	terminalAttempted = true
	completion, stop := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer stop()
	if err := access.append(completion, "complete", archive, ""); err != nil {
		return errors.Join(err, removeSupportArchive(parent, name, archive))
	}
	_, err = fmt.Fprintf(out, "INSTALL_SUPPORT_COMPLETE collection_issues=%d\n", issues)
	return err
}

func collectSupport(ctx context.Context, c Config, runner Runner, operationEnd int64) supportReport {
	report := supportReport{
		FormatVersion: 1, CollectedAt: time.Now().UTC(), Topology: c.Topology.Size,
		Host:          supportHost{OS: runtime.GOOS, Architecture: runtime.GOARCH},
		Configuration: supportConfig(c), Issues: []string{},
	}
	operations, err := collectSupportOperationsBefore(c, operationEnd)
	report.Operations = operations
	if err != nil {
		report.Issues = append(report.Issues, "OPERATION_LOG_UNAVAILABLE")
	}
	if intent, err := readRestoreIntent(c); err != nil {
		report.Issues = append(report.Issues, "RESTORE_STATE_UNAVAILABLE")
	} else if intent != nil {
		report.RestorePhase = intent.Phase
	}
	if intent, err := readCoordinatedBackupIntent(c); err != nil {
		report.Issues = append(report.Issues, "BACKUP_RECOVERY_STATE_UNAVAILABLE")
	} else if intent != nil {
		report.BackupRecovery = "recovery_required"
		if intent.FenceUntil != nil {
			report.BackupRecovery = "fence_outcome_unconfirmed"
		}
	}
	if raw, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		if version, ok := kernelRelease(strings.TrimSpace(string(raw))); ok {
			report.Host.Kernel = fmt.Sprintf("%d.%d.%d", version[0], version[1], version[2])
		}
	}
	if report.Host.Kernel == "" {
		report.Issues = append(report.Issues, "HOST_KERNEL_UNAVAILABLE")
	}
	var fs syscall.Statfs_t
	if err := syscall.Statfs(c.InstallRoot, &fs); err == nil && fs.Bsize > 0 {
		free := uint64(fs.Bavail) * uint64(fs.Bsize)
		report.Host.DataFreeBytes = &free
	} else {
		report.Issues = append(report.Issues, "INSTALL_FILESYSTEM_UNAVAILABLE")
	}
	for _, name := range []string{"control", "postgres", "metrics", "logs"} {
		report.Services = append(report.Services, supportService{Service: name, State: "unknown"})
	}
	if err := checkLocalDocker(ctx, runner); err != nil {
		report.Issues = append(report.Issues, "LOCAL_DOCKER_ENDPOINT_UNAVAILABLE")
		return report
	}
	if _, err := runner.Run(ctx, "docker", "version", "--format", "{{.Server.Version}}"); err != nil {
		report.Issues = append(report.Issues, "DOCKER_UNAVAILABLE")
		return report
	}
	report.Host.Docker = true
	i := installer{config: c, runner: runner}
	const format = `{"Service":{{json .Service}},"State":{{json .State}},"Health":{{json .Health}}}`
	raw, err := i.compose(ctx, "ps", "--all", "--format", format)
	if err != nil {
		report.Issues = append(report.Issues, "COMPOSE_STATE_UNAVAILABLE")
		return report
	}
	if err := readSupportServices(raw, report.Services); err != nil {
		report.Issues = append(report.Issues, "COMPOSE_STATE_INVALID")
		return report
	}
	for _, service := range report.Services {
		if service.Service != "postgres" || service.State != "running" {
			continue
		}
		raw, err := i.compose(ctx, "exec", "--no-TTY", "postgres", "psql", "-X", "-qAt", "-U", migratorRole, "-d", databaseName,
			"-v", "ON_ERROR_STOP=1", "-c", "SELECT max(version) FROM schema_migrations")
		version, parseError := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || parseError != nil || version < 1 {
			report.Issues = append(report.Issues, "DATABASE_SCHEMA_UNAVAILABLE")
		} else {
			report.SchemaVersion = &version
		}
	}
	return report
}

func readSupportServices(raw string, services []supportService) error {
	decoder := json.NewDecoder(strings.NewReader(raw))
	observed := map[string]supportService{}
	for {
		var item supportService
		if err := decoder.Decode(&item); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return err
		}
		switch item.Service {
		case "control", "postgres", "metrics", "logs":
		default:
			continue
		}
		if _, present := observed[item.Service]; present {
			return errors.New("unexpected duplicate Compose service")
		}
		switch item.State {
		case "created", "running", "paused", "restarting", "removing", "exited", "dead":
		default:
			item.State = "unknown"
		}
		switch item.Health {
		case "starting", "healthy", "unhealthy":
		default:
			item.Health = ""
		}
		observed[item.Service] = item
	}
	for n, service := range services {
		if item, ok := observed[service.Service]; ok {
			services[n] = item
		} else {
			services[n].State = "not_found"
		}
	}
	return nil
}

func writeSupport(parent *os.Root, name string, report any) (published supportArchive, result error) {
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return published, errors.New("INSTALL_SUPPORT_WRITE_FAILED: cannot encode the support report")
	}
	raw = append(raw, '\n')
	temporary := ".ownsolid-support-" + rand.Text()
	file, err := parent.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return published, errors.New("INSTALL_SUPPORT_WRITE_FAILED: check the output directory's permissions and free space")
	}
	defer parent.Remove(temporary)
	digest := sha256.New()
	compressed := gzip.NewWriter(io.MultiWriter(file, digest))
	archive := tar.NewWriter(compressed)
	if err = archive.WriteHeader(&tar.Header{Name: "support.json", Mode: 0o600, Size: int64(len(raw))}); err == nil {
		_, err = archive.Write(raw)
	}
	if err = errors.Join(err, archive.Close(), compressed.Close(), file.Sync(), file.Close()); err != nil {
		return published, errors.New("INSTALL_SUPPORT_WRITE_FAILED: cannot finish the archive; check free space and retry")
	}
	if err := parent.Link(temporary, name); errors.Is(err, os.ErrExist) {
		return published, errors.New("INSTALL_SUPPORT_OUTPUT_EXISTS: use an unused output path")
	} else if err != nil {
		return published, errors.New("INSTALL_SUPPORT_WRITE_FAILED: cannot publish the archive in this directory")
	}
	info, err := parent.Lstat(name)
	if err != nil {
		return published, errors.New("INSTALL_SUPPORT_WRITE_FAILED: cannot identify the published private archive")
	}
	published = supportArchive{sha256: hex.EncodeToString(digest.Sum(nil)), bytes: info.Size(), info: info}
	directory, err := parent.Open(".")
	if err != nil {
		return published, errors.New("INSTALL_SUPPORT_SYNC_FAILED: the archive was written but the directory could not be synchronized")
	}
	if err := errors.Join(directory.Sync(), directory.Close()); err != nil {
		return published, errors.New("INSTALL_SUPPORT_SYNC_FAILED: the archive was written but the directory could not be synchronized")
	}
	return published, nil
}
