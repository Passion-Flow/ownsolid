package components

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/discovery"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/progress"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/state"
)

type Config struct {
	RootDirectory string `json:"root_directory"`
	ServiceUser   string `json:"service_user"`
}
type Manager struct {
	config           Config
	runtime          *settings.Policy
	journalDirectory string
	unit             string
	service          serviceManager
	scrape           func(context.Context) (wire.ComponentMetrics, error)
	scrapeVersion    func(context.Context, string) (wire.ComponentMetrics, error)
}
type ownership struct {
	Version        string                `json:"version"`
	ArchiveSHA256  string                `json:"archive_sha256"`
	ServiceUser    string                `json:"service_user"`
	Files          map[string]string     `json:"files"`
	UnitSHA256     string                `json:"unit_sha256"`
	ManifestSHA256 string                `json:"manifest_sha256,omitempty"`
	Config         *wire.ComponentConfig `json:"config,omitempty"`
	InstalledAt    time.Time             `json:"installed_at,omitzero"`
}
type journal struct {
	Phase                string              `json:"phase"`
	Staging              string              `json:"staging"`
	UnitStaging          string              `json:"unit_staging"`
	Ownership            ownership           `json:"ownership"`
	Previous             *ownership          `json:"previous,omitempty"`
	Candidate            *ownership          `json:"candidate,omitempty"`
	CandidateCreated     bool                `json:"candidate_created,omitempty"`
	CommittedTaskID      string              `json:"committed_task_id,omitempty"`
	CommittedCommandHash string              `json:"committed_command_hash,omitempty"`
	LastMaintenanceError string              `json:"last_maintenance_error,omitempty"`
	LastMaintenanceCause string              `json:"last_maintenance_cause,omitempty"`
	CommittedLogs        *wire.ComponentLogs `json:"committed_logs,omitempty"`
}

var safePath = regexp.MustCompile(`^/[A-Za-z0-9_/-]+$`)
var safeUser = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

func code(v string) error { return errors.New(v) }
func New(config Config, journalDirectory string, policies ...settings.Policy) (*Manager, error) {
	policy := settings.Default()
	if len(policies) > 0 {
		policy = policies[0]
	}
	if e := policy.Validate(); e != nil {
		return nil, e
	}
	m := &Manager{runtime: &policy, config: config, journalDirectory: journalDirectory, unit: unitPath, service: systemd{runtime: &policy}}
	if !safePath.MatchString(config.RootDirectory) || filepath.Clean(config.RootDirectory) != config.RootDirectory || !safeUser.MatchString(config.ServiceUser) {
		return nil, code("COMPONENT_STATE_INVALID")
	}
	if e := m.Preflight(); e != nil {
		return nil, e
	}
	return m, nil
}
func (m *Manager) Preflight() error {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return code("COMPONENT_PROFILE_UNAVAILABLE")
	}
	if os.Geteuid() != 0 {
		return code("COMPONENT_PRIVILEGE_REQUIRED")
	}
	f, e := discovery.Collect("/")
	if e != nil || f.OSID != "ubuntu" || f.OSVersion != "24.04" {
		return code("COMPONENT_PROFILE_UNAVAILABLE")
	}
	if e = systemdAvailable(); e != nil {
		return e
	}
	u, e := user.Lookup(m.config.ServiceUser)
	if e != nil {
		return code("COMPONENT_USER_INVALID")
	}
	group, groupError := user.LookupGroup(m.config.ServiceUser)
	if groupError != nil || group.Gid != u.Gid {
		return code("COMPONENT_USER_INVALID")
	}
	uid, e := strconv.Atoi(u.Uid)
	if e != nil || uid < 1 || u.Username != m.config.ServiceUser {
		return code("COMPONENT_USER_INVALID")
	}
	if e = secureDirectory(m.config.RootDirectory); e != nil {
		return e
	}
	if e = state.Directory(m.journalDirectory); e != nil {
		return code("COMPONENT_STATE_INVALID")
	}
	if e = secureDirectory(filepath.Dir(m.unit)); e != nil {
		return e
	}
	return nil
}
func (m *Manager) final() string {
	return m.finalFor(wire.NodeExporterVersion)
}
func (m *Manager) finalFor(version string) string {
	return filepath.Join(m.config.RootDirectory, "node-exporter-"+version)
}
func (m *Manager) journalPath() string {
	return filepath.Join(m.journalDirectory, "node-exporter-journal.json")
}
func (m *Manager) unitBytes() []byte {
	return m.unitFor(ownership{Version: wire.NodeExporterVersion, ServiceUser: m.config.ServiceUser})
}
func (m *Manager) unitFor(o ownership) []byte {
	recipe, approved := wire.RecipeForVersion(o.Version)
	if !approved {
		return nil
	}
	flags := ""
	if o.Config != nil {
		flags = " --log.level=" + o.Config.LogLevel + " --web.max-requests=" + strconv.Itoa(o.Config.MaxScrapeRequests)
	}
	return []byte(fmt.Sprintf(`[Unit]
Description=Project-one managed Node Exporter
After=network.target
[Service]
Type=simple
User=%s
Group=%s
ExecStart=%s/node_exporter --web.listen-address=%s --collector.disable-defaults --collector.cpu --collector.meminfo --collector.filesystem --collector.uname --collector.filesystem.mount-points-include=^/$ --collector.filesystem.fs-types-exclude=^(autofs|binfmt_misc|bpf|cgroup2?|configfs|debugfs|devpts|devtmpfs|fusectl|hugetlbfs|mqueue|proc|pstore|rpc_pipefs|securityfs|sysfs|tracefs)$%s
Restart=on-failure
RestartSec=5s
TimeoutStartSec=15s
TimeoutStopSec=15s
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=true
ProtectSystem=strict
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true
MemoryMax=192M
CPUQuota=50%%
TasksMax=32
UMask=0027
[Install]
WantedBy=multi-user.target
`, o.ServiceUser, o.ServiceUser, m.finalFor(o.Version), recipe.ListenAddress, flags))
}
func bytesHash(b []byte) string { v := sha256.Sum256(b); return hex.EncodeToString(v[:]) }
func (m *Manager) save(j journal) error {
	if e := replaceJSON(m.journalPath(), j); e != nil {
		return code("COMPONENT_IO_FAILED")
	}
	return nil
}
func (m *Manager) readJournal() (journal, error) {
	var j journal
	b, e := state.ProtectedFile(m.journalPath())
	if e != nil {
		return j, code("COMPONENT_STATE_INVALID")
	}
	if json.Unmarshal(b, &j) != nil || (j.Phase != "installing" && j.Phase != "installed" && j.Phase != "removing" && !maintenancePhase(j.Phase)) || !m.validOwnership(j.Ownership) {
		return j, code("COMPONENT_STATE_INVALID")
	}
	if j.Previous != nil && (!m.validOwnership(*j.Previous) || len(j.Previous.Files) != 3) {
		return j, code("COMPONENT_STATE_INVALID")
	}
	if maintenancePhase(j.Phase) != (j.Candidate != nil) || j.Candidate != nil && !m.validOwnership(*j.Candidate) {
		return j, code("COMPONENT_STATE_INVALID")
	}
	if !wire.KnownMaintenanceError(j.LastMaintenanceError) || !wire.KnownMaintenanceError(j.LastMaintenanceCause) {
		return j, code("COMPONENT_STATE_INVALID")
	}
	if j.CommittedTaskID != "" {
		if !taskIdentity.MatchString(j.CommittedTaskID) || len(j.CommittedCommandHash) != 64 {
			return j, code("COMPONENT_STATE_INVALID")
		}
		if _, e := hex.DecodeString(j.CommittedCommandHash); e != nil {
			return j, code("COMPONENT_STATE_INVALID")
		}
	}
	if j.CommittedLogs != nil && j.CommittedLogs.Validate() != nil {
		return j, code("COMPONENT_STATE_INVALID")
	}
	if j.Staging != "" && (filepath.Dir(j.Staging) != m.config.RootDirectory || !regexp.MustCompile(`^\.node-exporter-stage-[0-9]+$`).MatchString(filepath.Base(j.Staging))) {
		return j, code("COMPONENT_STATE_INVALID")
	}
	if j.UnitStaging != "" {
		prefix := m.unit + ".staging-"
		candidatePath := j.UnitStaging
		if strings.HasSuffix(candidatePath, ".service") {
			candidatePath = strings.TrimSuffix(candidatePath, ".service")
		}
		if len(candidatePath) != len(prefix)+32 || candidatePath[:len(prefix)] != prefix {
			return j, code("COMPONENT_STATE_INVALID")
		}
		if _, e := hex.DecodeString(candidatePath[len(prefix):]); e != nil {
			return j, code("COMPONENT_STATE_INVALID")
		}
	}
	if j.Ownership.ManifestSHA256 == "" {
		j.Ownership.ManifestSHA256 = bytesHash(manifestBytes(j.Ownership))
	}
	if j.Previous != nil && j.Previous.ManifestSHA256 == "" {
		j.Previous.ManifestSHA256 = bytesHash(manifestBytes(*j.Previous))
	}
	return j, nil
}
func manifestBytes(own ownership) []byte {
	own.ManifestSHA256 = ""
	encoded, _ := json.Marshal(own)
	return encoded
}
func (m *Manager) validOwnership(o ownership) bool {
	recipe, ok := wire.RecipeForVersion(o.Version)
	if !ok || o.ArchiveSHA256 != recipe.ArchiveSHA256 || o.ServiceUser != m.config.ServiceUser {
		return false
	}
	if o.Config != nil && o.Config.Validate() != nil {
		return false
	}
	if o.Config == nil && o.Version != wire.NodeExporterVersion {
		return false
	}
	return o.UnitSHA256 == bytesHash(m.unitFor(o))
}
func (m *Manager) scrapeOwned(ctx context.Context, version string) (wire.ComponentMetrics, error) {
	if m.scrapeVersion != nil {
		return m.scrapeVersion(ctx, version)
	}
	if version == wire.NodeExporterVersion && m.scrape != nil {
		return m.scrape(ctx)
	}
	return ScrapeVersion(ctx, version, settings.Resolve(m.runtime))
}
func (m *Manager) verify(j journal, allowMissing bool) error {
	return m.verifyOwnership(j.Ownership, allowMissing, true)
}
func (m *Manager) verifyOwnership(o ownership, allowMissing, checkUnit bool) error {
	if !m.validOwnership(o) {
		return code("COMPONENT_STATE_INVALID")
	}
	final := m.finalFor(o.Version)
	if len(o.Files) != 3 {
		return code("COMPONENT_STATE_INVALID")
	}
	for _, name := range []string{"node_exporter", "LICENSE", "NOTICE"} {
		expected := o.Files[name]
		if len(expected) != 64 {
			return code("COMPONENT_STATE_INVALID")
		}
		hash, e := hashFile(filepath.Join(final, name))
		if allowMissing && os.IsNotExist(unwrapStat(filepath.Join(final, name))) {
			continue
		}
		if e != nil || hash != expected {
			return code("COMPONENT_CONFLICT")
		}
	}
	if e := secureDirectory(final); e != nil {
		if allowMissing && os.IsNotExist(unwrapStat(final)) {
			return nil
		}
		return e
	}
	entries, e := os.ReadDir(final)
	if e != nil {
		return code("COMPONENT_CONFLICT")
	}
	for _, entry := range entries {
		if _, ok := o.Files[entry.Name()]; !ok && entry.Name() != ".owned.json" {
			return code("COMPONENT_CONFLICT")
		}
	}
	expectedManifest := o.ManifestSHA256
	if expectedManifest == "" {
		expectedManifest = bytesHash(manifestBytes(o))
	}
	actualManifest, manifestError := hashFile(filepath.Join(final, ".owned.json"))
	if !(allowMissing && os.IsNotExist(unwrapStat(filepath.Join(final, ".owned.json")))) && (manifestError != nil || actualManifest != expectedManifest) {
		return code("COMPONENT_CONFLICT")
	}
	if !checkUnit {
		return nil
	}
	if _, e := os.Lstat(m.unit); os.IsNotExist(e) && allowMissing {
		return nil
	}
	hash, e := hashFile(m.unit)
	if e != nil || hash != o.UnitSHA256 {
		return code("COMPONENT_CONFLICT")
	}
	return nil
}
func unwrapStat(path string) error { _, e := os.Lstat(path); return e }
func (m *Manager) removeStaging(path string) error {
	if path == "" {
		return nil
	}
	if os.IsNotExist(unwrapStat(path)) {
		return nil
	}
	if e := secureDirectory(path); e != nil {
		return e
	}
	entries, e := os.ReadDir(path)
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if entry.Name() != "node_exporter" && entry.Name() != "LICENSE" && entry.Name() != "NOTICE" && entry.Name() != ".owned.json" {
			return code("COMPONENT_CONFLICT")
		}
		i, e := entry.Info()
		if e != nil || !i.Mode().IsRegular() || !rootOwned(i) {
			return code("COMPONENT_CONFLICT")
		}
	}
	for _, entry := range entries {
		if os.Remove(filepath.Join(path, entry.Name())) != nil {
			return code("COMPONENT_IO_FAILED")
		}
	}
	if os.Remove(path) != nil {
		return code("COMPONENT_IO_FAILED")
	}
	return syncDirectory(m.config.RootDirectory)
}
func (m *Manager) cleanup(ctx context.Context, j journal) error {
	if j.UnitStaging != "" && !os.IsNotExist(unwrapStat(j.UnitStaging)) {
		i, e := os.Lstat(j.UnitStaging)
		if e != nil || !i.Mode().IsRegular() || !rootOwned(i) || i.Mode().Perm()&0022 != 0 {
			return code("COMPONENT_CONFLICT")
		}
		if os.Remove(j.UnitStaging) != nil {
			return code("COMPONENT_IO_FAILED")
		}
		if e = syncDirectory(filepath.Dir(m.unit)); e != nil {
			return code("COMPONENT_IO_FAILED")
		}
	}
	download := filepath.Join(m.journalDirectory, ".node-exporter-download.tar.gz")
	if !os.IsNotExist(unwrapStat(download)) {
		i, e := os.Lstat(download)
		if e != nil || !i.Mode().IsRegular() || !rootOwned(i) || i.Mode().Perm()&0077 != 0 {
			return code("COMPONENT_CONFLICT")
		}
		if os.Remove(download) != nil {
			return code("COMPONENT_IO_FAILED")
		}
	}
	if !os.IsNotExist(unwrapStat(m.finalFor(j.Ownership.Version))) {
		if e := m.verify(j, true); e != nil {
			return e
		}
	}
	if j.Previous != nil {
		if e := m.verifyOwnership(*j.Previous, true, false); e != nil {
			return e
		}
	}
	if !os.IsNotExist(unwrapStat(m.unit)) {
		hash, e := hashFile(m.unit)
		if e != nil || hash != j.Ownership.UnitSHA256 {
			return code("COMPONENT_CONFLICT")
		}
		if e = m.service.Stop(ctx); e != nil {
			return e
		}
		if e = m.service.Disable(ctx); e != nil {
			return e
		}
		if os.Remove(m.unit) != nil {
			return code("COMPONENT_IO_FAILED")
		}
		if e = syncDirectory(filepath.Dir(m.unit)); e != nil {
			return code("COMPONENT_IO_FAILED")
		}
		if e = m.service.Reload(ctx); e != nil {
			return e
		}
	}
	if e := m.service.Reload(ctx); e != nil {
		return e
	}
	if e := m.removeStaging(m.finalFor(j.Ownership.Version)); e != nil {
		return e
	}
	if j.Previous != nil && j.Previous.Version != j.Ownership.Version {
		if e := m.removeStaging(m.finalFor(j.Previous.Version)); e != nil {
			return e
		}
	}
	if e := m.removeStaging(j.Staging); e != nil {
		return e
	}
	if e := os.Remove(m.journalPath()); e != nil && !os.IsNotExist(e) {
		return code("COMPONENT_IO_FAILED")
	}
	return syncDirectory(m.journalDirectory)
}
func (m *Manager) hasInstalledPaths() bool {
	for _, version := range []string{"1.12.0", wire.NodeExporterVersion} {
		if !os.IsNotExist(unwrapStat(m.finalFor(version))) {
			return true
		}
	}
	return false
}
func (m *Manager) Recover(ctx context.Context) error {
	if os.IsNotExist(unwrapStat(m.journalPath())) {
		if m.hasInstalledPaths() || !os.IsNotExist(unwrapStat(m.unit)) {
			return code("COMPONENT_CONFLICT")
		}
		return nil
	}
	j, e := m.readJournal()
	if e != nil {
		return e
	}
	if maintenancePhase(j.Phase) {
		return m.recoverMaintenance(ctx, j)
	}
	if j.Phase == "installed" {
		return m.verify(j, false)
	}
	return m.cleanup(ctx, j)
}

// Execute changes only the compiled recipe. The caller holds the agent's exclusive lock.
func (m *Manager) Execute(ctx context.Context, command wire.ComponentCommand, download func(context.Context, string) error) (wire.ComponentObservation, error) {
	if e := command.Validate(wire.ComponentOperation(command.Action)); e != nil {
		return m.failed(e), e
	}
	if e := m.Recover(ctx); e != nil {
		return m.failed(e), e
	}
	if ctx.Err() != nil {
		return m.failed(code("COMPONENT_CANCELLED")), code("COMPONENT_CANCELLED")
	}
	_, existsError := os.Lstat(m.journalPath())
	exists := existsError == nil
	var e error
	switch command.Action {
	case "install":
		if !exists {
			e = m.install(ctx, download)
		} else {
			progress.Step(ctx, wire.ProgressStepStart)
			e = m.service.Start(ctx)
		}
	case "start":
		if !exists {
			e = code("COMPONENT_STATE_INVALID")
		} else {
			progress.Step(ctx, wire.ProgressStepStart)
			e = m.service.Start(ctx)
		}
	case "stop":
		if !exists {
			e = code("COMPONENT_STATE_INVALID")
		} else {
			progress.Step(ctx, wire.ProgressStepStop)
			e = m.service.Stop(ctx)
		}
	case "remove":
		if exists {
			progress.Step(ctx, wire.ProgressStepRemove)
			var j journal
			j, e = m.readJournal()
			if e == nil {
				j.Phase = "removing"
				e = m.save(j)
			}
			if e == nil {
				e = m.cleanup(ctx, j)
			}
		}
	case "observe":
	}
	if e != nil {
		progress.Error(ctx, "Node exporter %s failed: %s", command.Action, safeComponentError(e).Error())
		return m.failed(e), safeComponentError(e)
	}
	progress.Step(ctx, wire.ProgressStepVerify)
	o := m.Observe(ctx)
	progress.Info(ctx, "Node exporter is %s", o.State)
	if command.Action == "install" || command.Action == "start" {
		if o.State != "running" || o.Metrics == nil {
			e = code("COMPONENT_SERVICE_FAILED")
		}
	}
	if command.Action == "stop" && o.State != "stopped" {
		e = code("COMPONENT_SERVICE_FAILED")
	}
	if e != nil {
		o.ErrorCode = e.Error()
		return o, e
	}
	if o.ErrorCode != "" {
		return o, code(o.ErrorCode)
	}
	return o, nil
}
func safeComponentError(e error) error {
	if e == nil {
		return nil
	}
	if wire.KnownMaintenanceError(e.Error()) && e.Error() != "" {
		return e
	}
	return code("COMPONENT_IO_FAILED")
}
func (m *Manager) failed(e error) wire.ComponentObservation {
	return wire.ComponentObservation{Component: wire.NodeExporter, Version: wire.NodeExporterVersion, State: "unknown", ObservedAt: time.Now().UTC(), ErrorCode: safeComponentError(e).Error()}
}
func (m *Manager) Observe(ctx context.Context) wire.ComponentObservation {
	o := wire.ComponentObservation{Component: wire.NodeExporter, Version: wire.NodeExporterVersion, State: "not_installed", ObservedAt: time.Now().UTC()}
	if os.IsNotExist(unwrapStat(m.journalPath())) {
		if m.hasInstalledPaths() || !os.IsNotExist(unwrapStat(m.unit)) {
			return m.failed(code("COMPONENT_CONFLICT"))
		}
		return o
	}
	j, e := m.readJournal()
	if e != nil || j.Phase != "installed" {
		return m.failed(code("COMPONENT_STATE_INVALID"))
	}
	if e = m.verify(j, false); e != nil {
		return m.failed(e)
	}
	o.Version = j.Ownership.Version
	o.Config = j.Ownership.Config
	if j.Previous != nil {
		if m.verifyOwnership(*j.Previous, false, false) == nil {
			o.PreviousVersion = j.Previous.Version
		} else {
			o.MaintenanceError = "COMPONENT_ROLLBACK_UNAVAILABLE"
		}
	}
	if j.LastMaintenanceError != "" {
		o.MaintenanceError = j.LastMaintenanceError
	}
	o.MaintenanceCause = j.LastMaintenanceCause
	o.Managed = true
	o.State, e = m.service.State(ctx)
	if e != nil {
		o.State = "unknown"
		o.ErrorCode = "COMPONENT_SERVICE_FAILED"
		return o
	}
	if o.State == "running" {
		metrics, e := m.scrapeOwned(ctx, j.Ownership.Version)
		if e != nil {
			o.ErrorCode = safeComponentError(e).Error()
		} else {
			o.Metrics = &metrics
		}
	}
	return o
}
func (m *Manager) install(ctx context.Context, download func(context.Context, string) error) error {
	return m.installVersion(ctx, wire.NodeExporterVersion, nil, download)
}
func (m *Manager) installVersion(ctx context.Context, version string, configuration *wire.ComponentConfig, download func(context.Context, string) error) error {
	recipe, approved := wire.RecipeForVersion(version)
	if !approved {
		return code("COMPONENT_VERSION_UNSUPPORTED")
	}
	if configuration == nil && version != wire.NodeExporterVersion {
		value := wire.DefaultComponentConfig()
		configuration = &value
	}
	if configuration != nil && configuration.Validate() != nil {
		return code("COMPONENT_CONFIG_INVALID")
	}
	own := ownership{Version: version, ArchiveSHA256: recipe.ArchiveSHA256, ServiceUser: m.config.ServiceUser, Config: configuration}
	if configuration != nil {
		own.InstalledAt = time.Now().UTC()
	}
	own.UnitSHA256 = bytesHash(m.unitFor(own))
	if e := ctx.Err(); e != nil {
		return code("COMPONENT_CANCELLED")
	}
	progress.Step(ctx, wire.ProgressStepPreflight)
	// Keep stop/recovery available under space pressure; staging headroom gates only new installs.
	available, e := diskAvailable(m.config.RootDirectory)
	if e != nil || available < 256*1024*1024 {
		return code("COMPONENT_DISK_SPACE_LOW")
	}
	// A new owned installation must not claim an existing listener or enablement link.
	listener, e := net.Listen("tcp", approvedListenAddress())
	if e != nil {
		return code("COMPONENT_PORT_CONFLICT")
	}
	listener.Close()
	if !os.IsNotExist(unwrapStat("/etc/systemd/system/multi-user.target.wants/" + unitName)) {
		return code("COMPONENT_CONFLICT")
	}
	archive := filepath.Join(m.journalDirectory, ".node-exporter-download.tar.gz")
	if !os.IsNotExist(unwrapStat(archive)) {
		return code("COMPONENT_STATE_INVALID")
	}
	stage, e := os.MkdirTemp(m.config.RootDirectory, ".node-exporter-stage-")
	if e != nil {
		return code("COMPONENT_IO_FAILED")
	}
	j := journal{Phase: "installing", Staging: stage, Ownership: own}
	if e = m.save(j); e != nil {
		os.Remove(stage)
		return e
	}
	defer os.Remove(archive)
	progress.Step(ctx, wire.ProgressStepDownload)
	progress.Info(ctx, "Fetching node exporter %s from control", version)
	if e = download(ctx, archive); e != nil {
		return safeComponentError(e)
	}
	progress.Step(ctx, wire.ProgressStepInstall)
	files, e := extractArchiveVersion(archive, stage, version)
	if e != nil {
		return e
	}
	progress.Info(ctx, "Unpacked %d files", len(files))
	j.Ownership.Files = files
	j.Ownership.ManifestSHA256 = bytesHash(manifestBytes(j.Ownership))
	if e = m.save(j); e != nil {
		return e
	}
	if ctx.Err() != nil {
		return code("COMPONENT_CANCELLED")
	}
	b := manifestBytes(j.Ownership)
	if e = writeNew(filepath.Join(stage, ".owned.json"), b, 0600); e != nil {
		return code("COMPONENT_IO_FAILED")
	}
	if e = os.Chmod(stage, 0755); e != nil {
		return code("COMPONENT_IO_FAILED")
	}
	if e = os.Rename(stage, m.finalFor(version)); e != nil {
		return code("COMPONENT_CONFLICT")
	}
	if e = syncDirectory(m.config.RootDirectory); e != nil {
		return code("COMPONENT_IO_FAILED")
	}
	var random [16]byte
	if _, e = rand.Read(random[:]); e != nil {
		return code("COMPONENT_IO_FAILED")
	}
	j.UnitStaging = m.unit + ".staging-" + hex.EncodeToString(random[:])
	if configuration != nil {
		j.UnitStaging += ".service"
	}
	if e = m.save(j); e != nil {
		return e
	}
	if e = writeNew(j.UnitStaging, m.unitFor(j.Ownership), 0644); e != nil {
		return code("COMPONENT_CONFLICT")
	}
	if configuration != nil {
		adapter, e := m.adapter()
		if e != nil {
			return e
		}
		if e = adapter.ValidateUnit(ctx, j.UnitStaging); e != nil {
			return e
		}
	}
	// The final unit appears atomically and refuses any pre-existing file or symlink.
	if e = os.Link(j.UnitStaging, m.unit); e != nil {
		return code("COMPONENT_CONFLICT")
	}
	if e = os.Remove(j.UnitStaging); e != nil {
		return code("COMPONENT_IO_FAILED")
	}
	if e = syncDirectory(filepath.Dir(m.unit)); e != nil {
		return code("COMPONENT_IO_FAILED")
	}
	if e = m.service.Reload(ctx); e != nil {
		return e
	}
	if ctx.Err() != nil {
		return code("COMPONENT_CANCELLED")
	}
	progress.Step(ctx, wire.ProgressStepStart)
	if e = m.service.Start(ctx); e != nil {
		return e
	}
	o := m.ObserveInstalling(ctx, j)
	if o.ErrorCode != "" {
		return code(o.ErrorCode)
	}
	j.Phase = "installed"
	j.Staging = ""
	j.UnitStaging = ""
	return m.save(j)
}
func (m *Manager) ObserveInstalling(ctx context.Context, j journal) wire.ComponentObservation {
	if e := m.verify(j, false); e != nil {
		return m.failed(e)
	}
	for i := 0; i < 15; i++ {
		if ctx.Err() != nil {
			return m.failed(code("COMPONENT_CANCELLED"))
		}
		actual, e := m.service.State(ctx)
		if e == nil && actual == "running" {
			if _, e = m.scrapeOwned(ctx, j.Ownership.Version); e == nil {
				return wire.ComponentObservation{}
			}
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return m.failed(code("COMPONENT_CANCELLED"))
		case <-timer.C:
		}
	}
	return m.failed(code("COMPONENT_SERVICE_FAILED"))
}

// DownloadTo writes only a new private file and rejects truncated or oversized data.
func DownloadTo(path string, source io.Reader) error {
	return DownloadToVersion(path, source, wire.NodeExporterVersion)
}
func DownloadToVersion(path string, source io.Reader, version string) error {
	recipe, approved := wire.RecipeForVersion(version)
	if !approved {
		return code("COMPONENT_VERSION_UNSUPPORTED")
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return code("COMPONENT_IO_FAILED")
	}
	n, e := io.Copy(f, io.LimitReader(source, recipe.ArchiveBytes+1))
	if e == nil && n != recipe.ArchiveBytes {
		e = code("COMPONENT_ASSET_INVALID")
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	return safeComponentError(e)
}

// RequiresMaintenance prevents a deliberately downgraded profile from silently using richer state.
func (m *Manager) RequiresMaintenance() (bool, error) {
	if os.IsNotExist(unwrapStat(m.journalPath())) {
		return false, nil
	}
	j, e := m.readJournal()
	if e != nil {
		return false, e
	}
	return j.Ownership.Config != nil || j.Previous != nil || maintenancePhase(j.Phase) || j.LastMaintenanceError != "" || j.CommittedLogs != nil, nil
}

func approvedListenAddress() string {
	recipe, _ := wire.RecipeForVersion(wire.NodeExporterVersion)
	return recipe.ListenAddress
}
