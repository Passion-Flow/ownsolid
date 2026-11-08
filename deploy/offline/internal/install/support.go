package install

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
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
	FormatVersion int              `json:"format_version"`
	CollectedAt   time.Time        `json:"collected_at"`
	Host          supportHost      `json:"host"`
	Topology      string           `json:"topology"`
	Services      []supportService `json:"services"`
	SchemaVersion *int             `json:"schema_version,omitempty"`
	Issues        []string         `json:"collection_issues"`
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
	collection, cancel := context.WithTimeout(ctx, time.Duration(c.StepSeconds)*time.Second)
	defer cancel()
	report := collectSupport(collection, c, runner)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("INSTALL_SUPPORT_CANCELLED: %w", err)
	}
	if errors.Is(collection.Err(), context.DeadlineExceeded) {
		report.Issues = append(report.Issues, "COLLECTION_TIMEOUT")
	}
	if err := writeSupport(parent, name, report); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "INSTALL_SUPPORT_COMPLETE collection_issues=%d\n", len(report.Issues))
	return err
}

func collectSupport(ctx context.Context, c Config, runner Runner) supportReport {
	report := supportReport{
		FormatVersion: 1, CollectedAt: time.Now().UTC(), Topology: c.Topology.Size,
		Host:   supportHost{OS: runtime.GOOS, Architecture: runtime.GOARCH},
		Issues: []string{},
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

func writeSupport(parent *os.Root, name string, report supportReport) error {
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return errors.New("INSTALL_SUPPORT_WRITE_FAILED: cannot encode the support report")
	}
	raw = append(raw, '\n')
	temporary := ".ownsolid-support-" + rand.Text()
	file, err := parent.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errors.New("INSTALL_SUPPORT_WRITE_FAILED: check the output directory's permissions and free space")
	}
	defer parent.Remove(temporary)
	compressed := gzip.NewWriter(file)
	archive := tar.NewWriter(compressed)
	if err = archive.WriteHeader(&tar.Header{Name: "support.json", Mode: 0o600, Size: int64(len(raw))}); err == nil {
		_, err = archive.Write(raw)
	}
	if err = errors.Join(err, archive.Close(), compressed.Close(), file.Sync(), file.Close()); err != nil {
		return errors.New("INSTALL_SUPPORT_WRITE_FAILED: cannot finish the archive; check free space and retry")
	}
	if err := parent.Link(temporary, name); errors.Is(err, os.ErrExist) {
		return errors.New("INSTALL_SUPPORT_OUTPUT_EXISTS: use an unused output path")
	} else if err != nil {
		return errors.New("INSTALL_SUPPORT_WRITE_FAILED: cannot publish the archive in this directory")
	}
	directory, err := parent.Open(".")
	if err != nil {
		return errors.New("INSTALL_SUPPORT_SYNC_FAILED: the archive was written but the directory could not be synchronized")
	}
	if err := errors.Join(directory.Sync(), directory.Close()); err != nil {
		return errors.New("INSTALL_SUPPORT_SYNC_FAILED: the archive was written but the directory could not be synchronized")
	}
	return nil
}
