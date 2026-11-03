package scope

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type mvpNativeNoReplayDouble struct {
	backupEngine
	starts          int
	observes        int
	completionReads int
	state           string
}

func TestMVPNativeBackupReconciliationHashesOriginalJournalPrefixBytes(t *testing.T) {
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	root, e := os.OpenRoot(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	p := mvpNativeSourcePlanFixture()
	l := &locked{root: root, config: configuration.Defaults(), marker: Marker{ScopeID: p.ScopeID, ConfigurationSHA256: p.ConfigurationSHA256}}
	var raw []byte
	previous := ""
	var prefixBytes int64
	var prefixSHA string
	for i := 0; i < 3; i++ {
		event := Event{FormatVersion: scopeFormatVersion, Sequence: i + 1, ScopeID: p.ScopeID, ConfigurationSHA256: p.ConfigurationSHA256, PreviousSHA256: previous, Action: "synthetic-readonly-byte-identity-fixture"}
		if i == 2 {
			event.Backup = &BackupRecord{AttemptID: p.AttemptID}
		}
		// Leading whitespace is valid retained JSON and changes original byte SHA.
		line := append([]byte(" "), encode(event)...)
		raw = append(raw, line...)
		previous = wire.Digest(bytes.TrimSuffix(line, []byte("\n")))
		l.events = append(l.events, event)
		if i == 1 {
			prefixBytes = int64(len(raw))
			prefixSHA = previous
		}
	}
	if e := writeBackupFile(root, journalName, raw); e != nil {
		t.Fatal(e)
	}
	originalHead := append([]byte(" "), encode(journalHead{FormatVersion: scopeFormatVersion, ScopeID: p.ScopeID, ConfigurationSHA256: p.ConfigurationSHA256, Sequence: 2, EventSHA256: prefixSHA, JournalBytes: prefixBytes})...)
	view, e := l.mvpNativeBackupAdmissionView(p.AttemptID, originalHead)
	if e != nil || !bytes.Equal(view.lastHead, originalHead) || view.lastSHA != prefixSHA || view.journalBytes != prefixBytes || len(view.events) != 2 {
		t.Fatal("original bytes reserialized", e)
	}
	var head journalHead
	if e := wire.StrictJSON(view.lastHead, &head); e != nil || head.EventSHA256 != prefixSHA || head.JournalBytes != prefixBytes {
		t.Fatal("original prefix head changed", e)
	}
	if e := root.Remove(journalName); e != nil {
		t.Fatal(e)
	}
	if e := writeBackupFile(root, journalName, bytes.TrimPrefix(raw, []byte(" "))); e != nil {
		t.Fatal(e)
	}
	if _, e := l.mvpNativeBackupAdmissionView(p.AttemptID, originalHead); e == nil {
		t.Fatal("changed original prefix accepted")
	}
}

func TestMVPNativeBackupMaintenanceHeadRetainsExactOriginalBytes(t *testing.T) {
	p := mvpNativeSourcePlanFixture()
	head := journalHead{FormatVersion: scopeFormatVersion, ScopeID: p.ScopeID, ConfigurationSHA256: p.ConfigurationSHA256, Sequence: 3, EventSHA256: strings.Repeat("a", 64), JournalBytes: 1000}
	raw := append([]byte(" "), encode(head)...)
	p.NativeRequest.MaintenanceSHA256 = wire.Digest(raw)
	if !validMVPNativeMaintenanceHead(p, raw) {
		t.Fatal("exact original valid head refused")
	}
	if validMVPNativeMaintenanceHead(p, bytes.TrimPrefix(raw, []byte(" "))) {
		t.Fatal("equivalent reserialized head replaced original bytes")
	}
	head.ScopeID = strings.Repeat("d", 32)
	raw = encode(head)
	p.NativeRequest.MaintenanceSHA256 = wire.Digest(raw)
	if validMVPNativeMaintenanceHead(p, raw) {
		t.Fatal("foreign original scope head accepted")
	}
}

func TestMVPNativeBackupReconcilesOnlyRetainedOriginalVerifierStdout(t *testing.T) {
	for _, fault := range []string{"none", "missing", "partial", "foreign-source"} {
		t.Run(fault, func(t *testing.T) {
			root, c, p, ref := mvpNativeCandidateFixture(t)
			p.Request.DestinationRoot = root.Name()
			if fault == "foreign-source" {
				ref.SourceManifestSHA256 = strings.Repeat("e", 64)
			}
			raw := encode(engine.NativePostgreSQLBackupObservation{Format: "projectone.postgres.independent-readback.v1", BackupID: p.AttemptID, RequestSHA256: ref.RequestSHA256, ManifestSHA256: ref.CandidateSHA256, ArchiveSHA256: strings.Repeat("d", 64), ArchiveDirectorySHA256: strings.Repeat("d", 64), ObservedAt: "2026-11-03T00:00:00Z", NativePostgreSQL: &ref})
			if fault == "partial" {
				raw = raw[:len(raw)/2]
			}
			if fault != "missing" {
				if e := writeBackupFile(root, mvpNativeObservationFile, raw); e != nil {
					t.Fatal(e)
				}
			}
			d := &mvpNativeNoReplayDouble{state: "exited"}
			l := &locked{config: c}
			task := engine.DatabaseTask{Kind: engine.NativePostgreSQLBackupVerify, NativePostgreSQLBackupVerifier: &engine.NativePostgreSQLBackupVerifierProfile{Request: p.NativeRequest, CandidateSHA256: ref.CandidateSHA256}}
			actual, e := l.executeMVPNativeTask(context.Background(), d, p, task, strings.Repeat("a", 64), nil, true)
			if fault == "none" {
				if e != nil || !bytes.Equal(actual, raw) {
					t.Fatal("retained original stdout not reopened", e)
				}
			} else if e == nil {
				t.Fatal("missing/changed observation repaired or accepted", fault)
			}
			if d.starts != 0 || d.observes != 0 || d.completionReads != 0 {
				t.Fatal("verifier replayed or replaced during reconciliation")
			}
		})
	}
}

func TestMVPNativeBackupExecutionRetainsExactlyOwnedTemporaryResources(t *testing.T) {
	p := mvpNativeSourcePlanFixture()
	fixture := func() mvpNativeBackupExecution {
		own := func(role string) engine.Ownership {
			return engine.Ownership{ScopeID: p.ScopeID, OperationID: p.AttemptID, Role: role + "-" + p.AttemptID}
		}
		roles := map[string]string{"native-input": engine.NativePostgreSQLRecoveryInputRole, "native-projection": engine.NativePostgreSQLConfigurationRole, "native-artifact": engine.NativePostgreSQLArtifactRole, "native-scratch": engine.NativePostgreSQLRecoveryScratchRole, "native-scaffold": engine.NativePostgreSQLScaffoldRole, "native-verify-input": engine.NativePostgreSQLVerifierInputRole, "native-verify-scratch": engine.NativePostgreSQLVerifierScratchRole}
		e := mvpNativeBackupExecution{Initializer: "sha256:" + strings.Repeat("a", 64), Volumes: map[string]BackupVolume{}, PayloadSHA256: map[string]string{}}
		for key, role := range roles {
			e.Volumes[key] = BackupVolume{Owner: own(role), UID: p.NativeUID, GID: p.NativeGID}
			e.PayloadSHA256[key] = strings.Repeat("a", 64)
		}
		profile := &engine.NativePostgreSQLRecoveryProfile{ConfigurationOwner: own(engine.NativePostgreSQLConfigurationRole), BackupOwner: own(engine.NativePostgreSQLArtifactRole), ScratchOwner: own(engine.NativePostgreSQLRecoveryScratchRole), Scaffold: &engine.NativePostgreSQLVerifierPlaceholder{Owner: own(engine.NativePostgreSQLScaffoldRole)}, Registry: &engine.NativePostgreSQLRegistryMount{Owner: engine.Ownership{ScopeID: p.ScopeID, Role: engine.NativePostgreSQLRegistryRole}}}
		for i, role := range []string{"models", "datasets", "training", "preparations"} {
			profile.Stores[i].Owner = engine.Ownership{ScopeID: p.ScopeID, Role: role}
		}
		e.Writer = engine.DatabaseTask{VolumeOwner: own(engine.NativePostgreSQLRecoveryInputRole), NativePostgreSQLRecovery: profile}
		return e
	}
	if !validMVPNativeExecutionOwners(p, fixture()) {
		t.Fatal("fixed ownership fixture rejected")
	}
	for name, change := range map[string]func(*mvpNativeBackupExecution){
		"extra cleanup owner":   func(e *mvpNativeBackupExecution) { e.Volumes["foreign"] = BackupVolume{} },
		"missing cleanup owner": func(e *mvpNativeBackupExecution) { delete(e.Volumes, "native-artifact") },
		"changed UID": func(e *mvpNativeBackupExecution) {
			v := e.Volumes["native-input"]
			v.UID++
			e.Volumes["native-input"] = v
		},
		"foreign temporary owner": func(e *mvpNativeBackupExecution) {
			v := e.Volumes["native-artifact"]
			v.Owner.ScopeID = strings.Repeat("d", 32)
			e.Volumes["native-artifact"] = v
		},
		"missing payload hash": func(e *mvpNativeBackupExecution) { delete(e.PayloadSHA256, "native-projection") },
		"foreign original store": func(e *mvpNativeBackupExecution) {
			e.Writer.NativePostgreSQLRecovery.Stores[0].Owner.ScopeID = strings.Repeat("d", 32)
		},
		"foreign original registry": func(e *mvpNativeBackupExecution) {
			e.Writer.NativePostgreSQLRecovery.Registry.Owner.ScopeID = strings.Repeat("d", 32)
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := fixture()
			change(&e)
			if validMVPNativeExecutionOwners(p, e) {
				t.Fatal("changed ownership accepted")
			}
		})
	}
}

func (d *mvpNativeNoReplayDouble) InspectDatabaseTask(context.Context, engine.DatabaseTask) (engine.RuntimeContainer, bool, error) {
	v := engine.RuntimeContainer{ID: strings.Repeat("a", 64)}
	v.State.Status = d.state
	return v, true, nil
}
func (d *mvpNativeNoReplayDouble) ObserveDatabaseTaskOutput(context.Context, engine.DatabaseTask, string) (*engine.TaskOutputObserver, error) {
	d.observes++
	return nil, errors.New("explicit observer unavailable fixture")
}
func (d *mvpNativeNoReplayDouble) StartDatabaseTask(context.Context, engine.DatabaseTask, string, []engine.ConfigurationFile, int) (engine.ResourceAdmission, error) {
	d.starts++
	return engine.ResourceAdmission{}, errors.New("start forbidden in readonly reconciliation fixture")
}
func (d *mvpNativeNoReplayDouble) ReadNativePostgreSQLTaskCompletion(context.Context, engine.DatabaseTask, string) (engine.TaskOutputObservation, error) {
	d.completionReads++
	return engine.TaskOutputObservation{}, errors.New("original completion unavailable fixture")
}
func (d *mvpNativeNoReplayDouble) StreamNativePostgreSQLBackupArchive(context.Context, engine.DatabaseTask, string, int64, int, int, io.Writer) (engine.NativePostgreSQLArchive, error) {
	return engine.NativePostgreSQLArchive{}, errors.New("unused")
}
func (d *mvpNativeNoReplayDouble) PutNativePostgreSQLScaffold(context.Context, engine.DatabaseTask, string) (engine.ConfigurationObservation, error) {
	return engine.ConfigurationObservation{}, errors.New("unused")
}
func (d *mvpNativeNoReplayDouble) InspectNativePostgreSQLScaffold(context.Context, engine.DatabaseTask, string) (engine.ConfigurationObservation, error) {
	return engine.ConfigurationObservation{}, errors.New("unused")
}
func (d *mvpNativeNoReplayDouble) RemoveNativePostgreSQLVolume(context.Context, engine.Ownership) error {
	return errors.New("unused")
}

func TestMVPNativeBackupUnknownAndMissingOriginalObservationNeverResend(t *testing.T) {
	for _, kind := range []string{engine.NativePostgreSQLBackup, engine.NativePostgreSQLBackupVerify} {
		for _, state := range []string{"created", "exited"} {
			t.Run(kind+"/"+state, func(t *testing.T) {
				d := &mvpNativeNoReplayDouble{state: state}
				l := &locked{}
				p := mvpNativeSourcePlanFixture()
				if _, e := l.executeMVPNativeTask(context.Background(), d, p, engine.DatabaseTask{Kind: kind}, strings.Repeat("a", 64), nil, true); e == nil {
					t.Fatal("unknown/missing original proof accepted")
				}
				if d.starts != 0 || d.observes != 0 {
					t.Fatal("original native task resent", d.starts, d.observes)
				}
				if kind == engine.NativePostgreSQLBackup && state == "exited" && d.completionReads != 1 {
					t.Fatal("original completion not independently observed")
				}
			})
		}
	}
}

func TestMVPNativeBackupExecutionUsesActualFixedStepsAndExclusiveVerifier(t *testing.T) {
	e := mvpNativeBackupExecution{Volumes: map[string]BackupVolume{}}
	for _, role := range []string{"native-input", "native-projection", "native-artifact", "native-scratch", "native-scaffold", "native-verify-input", "native-verify-scratch"} {
		e.Volumes[role] = BackupVolume{}
	}
	steps := mvpNativeBackupSteps(e)
	writerStarts, verifierStarts := 0, 0
	writerRemoved, verifierCreated := -1, -1
	for i, s := range steps {
		if s.Phase == "task-execute" && s.Role == "native-writer" {
			writerStarts++
		}
		if s.Phase == "task-execute" && s.Role == "native-verifier" {
			verifierStarts++
		}
		if s.Phase == "task-remove" && s.Role == "native-writer" {
			writerRemoved = i
		}
		if s.Phase == "task-create" && s.Role == "native-verifier" {
			verifierCreated = i
		}
		if s.Role == "postgres" || s.Role == "models" || s.Role == "datasets" || s.Role == "training" || s.Role == "preparations" {
			t.Fatal("legacy split backup task introduced")
		}
	}
	if len(steps) != 46 || writerStarts != 1 || verifierStarts != 1 || writerRemoved < 0 || writerRemoved >= verifierCreated || steps[0].Phase != "reserve" || steps[len(steps)-1].Phase != "complete" {
		t.Fatal("actual fixed task/cleanup ordering changed", len(steps), writerStarts, verifierStarts, writerRemoved, verifierCreated)
	}
}
