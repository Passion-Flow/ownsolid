//go:build linux && component_integration && maintenance_integration

package components

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// This adapter is a test-only external-state model. It does not qualify real systemd.
type syntheticMaintenanceSystemd struct {
	*syntheticSystemd
	unit, version           string
	restarts, journalReads  int
	reloads                 int
	loadedUnit, runningUnit []byte
	badHealth               string
	cancelNew               context.CancelFunc
	cancelRestart           context.CancelFunc
	cancelReload            context.CancelFunc
	failReload, failRestart bool
	changeUnit              bool
}

func (s *syntheticMaintenanceSystemd) Reload(ctx context.Context) error {
	if ctx.Err() != nil {
		return code("COMPONENT_CANCELLED")
	}
	s.reloads++
	if s.failReload {
		s.failReload = false
		return code("COMPONENT_SERVICE_FAILED")
	}
	body, e := os.ReadFile(s.unit)
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	s.loadedUnit = body
	if s.cancelReload != nil {
		cancel := s.cancelReload
		s.cancelReload = nil
		cancel()
	}
	return nil
}
func (s *syntheticMaintenanceSystemd) loadVersion() error {
	match := regexp.MustCompile(`/node-exporter-([0-9.]+)/node_exporter`).FindStringSubmatch(string(s.loadedUnit))
	if len(match) != 2 {
		return code("COMPONENT_SERVICE_FAILED")
	}
	s.version = match[1]
	s.runningUnit = bytes.Clone(s.loadedUnit)
	return nil
}
func (s *syntheticMaintenanceSystemd) Start(ctx context.Context) error {
	if e := s.loadVersion(); e != nil {
		return e
	}
	return s.syntheticSystemd.Start(ctx)
}
func (s *syntheticMaintenanceSystemd) Restart(ctx context.Context) error {
	if ctx.Err() != nil {
		return code("COMPONENT_CANCELLED")
	}
	s.restarts++
	if s.failRestart {
		s.failRestart = false
		return code("COMPONENT_SERVICE_FAILED")
	}
	if e := s.loadVersion(); e != nil {
		return e
	}
	s.state = "running"
	if s.version == "1.12.1" && s.cancelNew != nil {
		s.cancelNew()
		s.cancelNew = nil
	}
	if s.cancelRestart != nil {
		cancel := s.cancelRestart
		s.cancelRestart = nil
		cancel()
	}
	return nil
}
func (s *syntheticMaintenanceSystemd) ValidateUnit(ctx context.Context, path string) error {
	if _, e := os.ReadFile(path); e != nil {
		return e
	}
	if s.changeUnit {
		s.changeUnit = false
		return os.WriteFile(s.unit, []byte("unowned changed unit sentinel"), 0644)
	}
	return nil
}
func (s *syntheticMaintenanceSystemd) Journal(ctx context.Context, since time.Time) (wire.ComponentLogs, error) {
	s.journalReads++
	now := time.Now().UTC()
	body, _ := json.Marshal(map[string]string{"MESSAGE": "Synthetic test-only journal <entry>", "PRIORITY": "6", "__REALTIME_TIMESTAMP": strconv.FormatInt(now.UnixMicro(), 10)})
	return parseJournal(body, since, now)
}
func maintenanceFixture(t *testing.T) (*Manager, *syntheticMaintenanceSystemd) {
	t.Helper()
	m, base, _ := testManager(t)
	service := &syntheticMaintenanceSystemd{syntheticSystemd: base, unit: m.unit}
	m.service = service
	m.scrapeVersion = func(ctx context.Context, version string) (wire.ComponentMetrics, error) {
		if service.version != version || service.badHealth == version {
			return wire.ComponentMetrics{}, code("COMPONENT_METRICS_UNAVAILABLE")
		}
		return ParseMetrics([]byte(syntheticMetrics))
	}
	return m, service
}
func versionDownload(version string) func(context.Context, string) error {
	return func(ctx context.Context, path string) error {
		archive := os.Getenv("PROJECT_ONE_COMPONENT_ARCHIVE")
		if version == "1.12.0" {
			archive = os.Getenv("PROJECT_ONE_COMPONENT_ARCHIVE_V120")
		}
		f, e := os.Open(archive)
		if e != nil {
			return e
		}
		defer f.Close()
		return DownloadToVersion(path, f, version)
	}
}
func maintenanceCommand(t *testing.T, action, version, expected string, config *wire.ComponentConfig) wire.ComponentCommand {
	t.Helper()
	c, e := wire.MaintenanceCommand(action, version, expected, config)
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func executeMaintenance(t *testing.T, m *Manager, id, action, version, expected string, config *wire.ComponentConfig) wire.ComponentObservation {
	t.Helper()
	o, _, e := m.ExecuteMaintenance(context.Background(), id, maintenanceCommand(t, action, version, expected, config), versionDownload(version))
	if e != nil {
		t.Fatalf("%s failed: %v", action, e)
	}
	return o
}
func installPrevious(t *testing.T, m *Manager) {
	executeMaintenance(t, m, "00000000-0000-0000-0000-000000000001", "install", "1.12.0", "", nil)
}

func TestM05JournaledConfigureUpgradeRollbackAndCommittedReplay(t *testing.T) {
	m, s := maintenanceFixture(t)
	installPrevious(t, m)
	configuration := wire.ComponentConfig{LogLevel: "warn", MaxScrapeRequests: 2}
	o := executeMaintenance(t, m, "00000000-0000-0000-0000-000000000002", "configure", "1.12.0", "1.12.0", &configuration)
	if o.Config == nil || *o.Config != configuration || o.PreviousVersion != "1.12.0" {
		t.Fatal("applied/prior configuration missing")
	}
	executeMaintenance(t, m, "00000000-0000-0000-0000-000000000003", "restart", "1.12.0", "1.12.0", nil)
	logsCommand := maintenanceCommand(t, "logs", "1.12.0", "1.12.0", nil)
	_, logs, e := m.ExecuteMaintenance(context.Background(), "00000000-0000-0000-0000-000000000004", logsCommand, versionDownload("1.12.0"))
	if e != nil || logs == nil || len(logs.Entries) != 1 {
		t.Fatal("latest bounded synthetic logs missing", e)
	}
	_, replayed, e := m.ExecuteMaintenance(context.Background(), "00000000-0000-0000-0000-000000000004", logsCommand, versionDownload("1.12.0"))
	if e != nil || s.journalReads != 1 || replayed == nil || !replayed.ObservedAt.Equal(logs.ObservedAt) {
		t.Fatal("committed log response not reconciled", e)
	}
	o = executeMaintenance(t, m, "00000000-0000-0000-0000-000000000005", "upgrade", "1.12.1", "1.12.0", nil)
	if o.Version != "1.12.1" || o.PreviousVersion != "1.12.0" || o.Config == nil || *o.Config != configuration {
		t.Fatal("forward upgrade lost configuration/snapshot")
	}
	before := s.restarts
	executeMaintenance(t, m, "00000000-0000-0000-0000-000000000005", "upgrade", "1.12.1", "1.12.0", nil)
	if s.restarts != before {
		t.Fatal("committed upgrade repeated its external effect")
	}
	o = executeMaintenance(t, m, "00000000-0000-0000-0000-000000000006", "rollback", "1.12.0", "1.12.1", nil)
	if o.Version != "1.12.0" || o.PreviousVersion != "1.12.1" {
		t.Fatal("rollback did not restore prior actual version")
	}
	before = s.restarts
	executeMaintenance(t, m, "00000000-0000-0000-0000-000000000006", "rollback", "1.12.0", "1.12.1", nil)
	if s.restarts != before {
		t.Fatal("rollback replay toggled the retained snapshots")
	}
	executeMaintenance(t, m, "00000000-0000-0000-0000-000000000007", "upgrade", "1.12.1", "1.12.0", nil)
	executeMaintenance(t, m, "00000000-0000-0000-0000-000000000008", "remove", "1.12.1", "1.12.1", nil)
	if m.hasInstalledPaths() || !os.IsNotExist(unwrapStat(m.unit)) {
		t.Fatal("verified owned current/prior artifacts survived removal")
	}
	for _, name := range []string{"PROJECT_ONE_COMPONENT_ARCHIVE", "PROJECT_ONE_COMPONENT_ARCHIVE_V120"} {
		if _, e = os.Stat(os.Getenv(name)); e != nil {
			t.Fatal("shared archive was removed")
		}
	}
}
func TestM05FailedHealthRollsBackAndRetainsCause(t *testing.T) {
	m, s := maintenanceFixture(t)
	installPrevious(t, m)
	s.badHealth = "1.12.1"
	o, _, e := m.ExecuteMaintenance(context.Background(), "00000000-0000-0000-0000-000000000002", maintenanceCommand(t, "upgrade", "1.12.1", "1.12.0", nil), versionDownload("1.12.1"))
	if e == nil || e.Error() != "COMPONENT_UPDATE_ROLLED_BACK" || o.Version != "1.12.0" || o.State != "running" || o.MaintenanceCause != "COMPONENT_SERVICE_FAILED" {
		t.Fatal("failed health was not honestly rolled back", e)
	}
	if !os.IsNotExist(unwrapStat(m.finalFor("1.12.1"))) {
		t.Fatal("new failed owned candidate survived safe recovery")
	}
	j, e := m.readJournal()
	if e != nil || j.Phase != "installed" || j.Ownership.Version != "1.12.0" || j.Candidate != nil {
		t.Fatal("rollback journal did not commit", e)
	}
}
func TestM05InterruptedSwitchRecoversWithFreshManager(t *testing.T) {
	m, s := maintenanceFixture(t)
	installPrevious(t, m)
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelNew = cancel
	_, _, e := m.ExecuteMaintenance(ctx, "00000000-0000-0000-0000-000000000002", maintenanceCommand(t, "upgrade", "1.12.1", "1.12.0", nil), versionDownload("1.12.1"))
	if e == nil || e.Error() != "COMPONENT_CANCELLED" {
		t.Fatal("interruption was not persisted", e)
	}
	fresh := *m
	if e = fresh.Recover(context.Background()); e != nil {
		t.Fatal("fresh manager could not recover durable switch", e)
	}
	o := fresh.Observe(context.Background())
	if o.Version != "1.12.0" || o.State != "running" || o.MaintenanceError != "COMPONENT_UPDATE_ROLLED_BACK" || o.MaintenanceCause != "COMPONENT_INTERRUPTED" {
		t.Fatal("recovery fabricated or lost prior state")
	}
	executeMaintenance(t, &fresh, "00000000-0000-0000-0000-000000000002", "upgrade", "1.12.1", "1.12.0", nil)
}

func interruptedConfiguration(t *testing.T) (*Manager, *syntheticMaintenanceSystemd, []byte) {
	t.Helper()
	m, s := maintenanceFixture(t)
	installPrevious(t, m)
	prior := bytes.Clone(s.runningUnit)
	configuration := wire.ComponentConfig{LogLevel: "warn", MaxScrapeRequests: 2}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.cancelRestart = cancel
	_, _, e := m.ExecuteMaintenance(ctx, "00000000-0000-0000-0000-000000000002", maintenanceCommand(t, "configure", "1.12.0", "1.12.0", &configuration), versionDownload("1.12.0"))
	if e == nil || e.Error() != "COMPONENT_CANCELLED" {
		t.Fatal("configuration interruption was not retained", e)
	}
	j, e := m.readJournal()
	if e != nil || j.Phase != "health" || j.Candidate == nil {
		t.Fatal("interrupted configuration lost its recovery journal", e)
	}
	if bytes.Equal(prior, s.runningUnit) || bytesHash(s.runningUnit) != j.Candidate.UnitSHA256 {
		t.Fatal("fixture did not run the candidate configuration")
	}
	if _, e = m.scrapeOwned(context.Background(), j.Ownership.Version); e != nil {
		t.Fatal("same-version metrics must remain healthy across this configuration change", e)
	}
	return m, s, prior
}

func assertRecoveredConfiguration(t *testing.T, m *Manager, s *syntheticMaintenanceSystemd, prior []byte) {
	t.Helper()
	body, e := os.ReadFile(m.unit)
	if e != nil || !bytes.Equal(body, prior) || !bytes.Equal(s.loadedUnit, prior) || !bytes.Equal(s.runningUnit, prior) {
		t.Fatal("prior disk, loaded unit and running configuration were not restored together", e)
	}
	j, e := m.readJournal()
	if e != nil || j.Phase != "installed" || j.Candidate != nil {
		t.Fatal("recovered configuration did not settle its journal", e)
	}
	o := m.Observe(context.Background())
	if o.State != "running" || o.Version != "1.12.0" || o.Config == nil || *o.Config != wire.DefaultComponentConfig() || o.MaintenanceError != "COMPONENT_UPDATE_ROLLED_BACK" || o.MaintenanceCause != "COMPONENT_INTERRUPTED" {
		t.Fatal("reported configuration does not describe the recovered process")
	}
	if j.CommittedTaskID != "00000000-0000-0000-0000-000000000001" {
		t.Fatal("cancelled configuration was recorded as a committed task")
	}
}

func TestM05InterruptedConfigureRestoresRunningConfiguration(t *testing.T) {
	m, s, prior := interruptedConfiguration(t)
	fresh := *m
	reloads, restarts := s.reloads, s.restarts
	if e := fresh.Recover(context.Background()); e != nil {
		t.Fatal(e)
	}
	if s.reloads != reloads+1 || s.restarts != restarts+1 {
		t.Fatal("configuration rollback skipped manager reload or process restart")
	}
	assertRecoveredConfiguration(t, &fresh, s, prior)
	reloads, restarts = s.reloads, s.restarts
	if e := fresh.Recover(context.Background()); e != nil || s.reloads != reloads || s.restarts != restarts {
		t.Fatal("settled recovery repeated external effects", e)
	}
}

func TestM05ConfigurationRecoveryRetriesRestoredUnitAfterFault(t *testing.T) {
	for _, fault := range []string{"reload_failure", "restart_failure", "cancel_after_reload", "cancel_after_restart"} {
		t.Run(fault, func(t *testing.T) {
			m, s, prior := interruptedConfiguration(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch fault {
			case "reload_failure":
				s.failReload = true
			case "restart_failure":
				s.failRestart = true
			case "cancel_after_reload":
				s.cancelReload = cancel
			case "cancel_after_restart":
				s.cancelRestart = cancel
			}
			fresh := *m
			if e := fresh.Recover(ctx); e == nil {
				t.Fatal("incomplete recovery was reported as settled")
			}
			j, e := fresh.readJournal()
			if e != nil || j.Phase != "rolling_back" || j.Candidate == nil {
				t.Fatal("recovery failure lost the transition journal", e)
			}
			body, e := os.ReadFile(fresh.unit)
			if e != nil || !bytes.Equal(body, prior) {
				t.Fatal("fault did not occur after the prior unit was restored", e)
			}
			reloads, restarts := s.reloads, s.restarts
			restarted := fresh
			if e = restarted.Recover(context.Background()); e != nil {
				t.Fatal("fresh recovery failed with the original unit already on disk", e)
			}
			if s.reloads != reloads+1 || s.restarts != restarts+1 {
				t.Fatal("recovery inferred process configuration from the restored file or version")
			}
			assertRecoveredConfiguration(t, &restarted, s, prior)
		})
	}
}

func TestM05InterruptedStagingPreservesRunningConfiguration(t *testing.T) {
	m, s := maintenanceFixture(t)
	installPrevious(t, m)
	prior := bytes.Clone(s.runningUnit)
	j, e := m.readJournal()
	if e != nil {
		t.Fatal(e)
	}
	candidate := j.Ownership
	configuration := wire.ComponentConfig{LogLevel: "warn", MaxScrapeRequests: 2}
	candidate.Config = &configuration
	candidate.UnitSHA256 = bytesHash(m.unitFor(candidate))
	j.Candidate = &candidate
	j.Phase = "updating"
	if e = m.save(j); e != nil {
		t.Fatal(e)
	}
	reloads, restarts := s.reloads, s.restarts
	fresh := *m
	if e = fresh.Recover(context.Background()); e != nil {
		t.Fatal(e)
	}
	if s.reloads != reloads || s.restarts != restarts {
		t.Fatal("unswitched staging restarted the unchanged healthy process")
	}
	assertRecoveredConfiguration(t, &fresh, s, prior)
}
func TestM05ChangedUnitAndPriorFilesRemainPreserved(t *testing.T) {
	m, s := maintenanceFixture(t)
	installPrevious(t, m)
	original, e := os.ReadFile(m.unit)
	if e != nil {
		t.Fatal(e)
	}
	s.changeUnit = true
	_, _, e = m.ExecuteMaintenance(context.Background(), "00000000-0000-0000-0000-000000000002", maintenanceCommand(t, "upgrade", "1.12.1", "1.12.0", nil), versionDownload("1.12.1"))
	if e == nil || e.Error() != "COMPONENT_ROLLBACK_FAILED" {
		t.Fatal("changed unit was overwritten", e)
	}
	if body, _ := os.ReadFile(m.unit); string(body) != "unowned changed unit sentinel" {
		t.Fatal("unowned changed unit was not preserved")
	}
	// An explicitly simulated operator repair restores only this fixture's original owned unit.
	if e = os.WriteFile(m.unit, original, 0644); e != nil {
		t.Fatal(e)
	}
	if e = m.Recover(context.Background()); e != nil {
		t.Fatal(e)
	}
	executeMaintenance(t, m, "00000000-0000-0000-0000-000000000003", "upgrade", "1.12.1", "1.12.0", nil)
	priorLicense := filepath.Join(m.finalFor("1.12.0"), "LICENSE")
	if e = os.WriteFile(priorLicense, []byte("changed prior sentinel"), 0644); e != nil {
		t.Fatal(e)
	}
	_, _, e = m.ExecuteMaintenance(context.Background(), "00000000-0000-0000-0000-000000000004", maintenanceCommand(t, "rollback", "1.12.0", "1.12.1", nil), versionDownload("1.12.0"))
	if e == nil || e.Error() != "COMPONENT_ROLLBACK_UNAVAILABLE" {
		t.Fatal("changed prior artifact accepted", e)
	}
	if body, _ := os.ReadFile(priorLicense); !strings.Contains(string(body), "changed prior sentinel") {
		t.Fatal("changed prior file deleted")
	}
}

func TestM05LegacyOwnedUnitCanReconcileConfigureAndRestoreDefaults(t *testing.T) {
	m, _ := maintenanceFixture(t)
	if e := m.install(context.Background(), versionDownload(wire.NodeExporterVersion)); e != nil {
		t.Fatal("legacy owned install failed", e)
	}
	defaults := wire.DefaultComponentConfig()
	executeMaintenance(t, m, "00000000-0000-0000-0000-000000000001", "install", "1.12.1", "1.12.1", &defaults)
	configuration := wire.ComponentConfig{LogLevel: "warn", MaxScrapeRequests: 2}
	executeMaintenance(t, m, "00000000-0000-0000-0000-000000000002", "configure", "1.12.1", "1.12.1", &configuration)
	o := executeMaintenance(t, m, "00000000-0000-0000-0000-000000000003", "rollback", "1.12.1", "1.12.1", nil)
	if o.Config != nil || o.Version != "1.12.1" || o.State != "running" {
		t.Fatal("verified legacy default unit was not restored")
	}
	unit, e := os.ReadFile(m.unit)
	if e != nil || strings.Contains(string(unit), "--log.level=") {
		t.Fatal("legacy rendering was silently replaced", e)
	}
}
