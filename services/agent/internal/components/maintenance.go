package components

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/progress"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

var taskIdentity = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

func maintenancePhase(phase string) bool {
	return phase == "updating" || phase == "switching" || phase == "health" || phase == "rolling_back"
}
func commandHash(command wire.ComponentCommand) string {
	value, _ := json.Marshal(command)
	return bytesHash(value)
}
func appliedConfig(own ownership) wire.ComponentConfig {
	if own.Config != nil {
		return *own.Config
	}
	return wire.DefaultComponentConfig()
}
func (m *Manager) adapter() (maintenanceService, error) {
	value, ok := m.service.(maintenanceService)
	if !ok {
		return nil, code("COMPONENT_MAINTENANCE_REQUIRED")
	}
	return value, nil
}
func (m *Manager) ownedSince(own ownership) (time.Time, error) {
	if !own.InstalledAt.IsZero() {
		return own.InstalledAt, nil
	}
	info, e := os.Lstat(filepath.Join(m.finalFor(own.Version), ".owned.json"))
	if e != nil || !info.Mode().IsRegular() || !rootOwned(info) {
		return time.Time{}, code("COMPONENT_STATE_INVALID")
	}
	return info.ModTime().UTC(), nil
}

// ExecuteMaintenance executes the approved maintenance contract under the agent's lock.
// Enrollment authority is checked by the runtime before this method is reachable.
func (m *Manager) ExecuteMaintenance(ctx context.Context, taskID string, command wire.ComponentCommand, download func(context.Context, string) error) (wire.ComponentObservation, *wire.ComponentLogs, error) {
	failure := func(e error) (wire.ComponentObservation, *wire.ComponentLogs, error) {
		observation := m.Observe(ctx)
		observation.ErrorCode = safeComponentError(e).Error()
		return observation, nil, safeComponentError(e)
	}
	if !taskIdentity.MatchString(taskID) {
		return failure(code("COMPONENT_STATE_INVALID"))
	}
	if e := command.ValidateMaintenance(wire.ComponentOperation(command.Action)); e != nil {
		return failure(e)
	}
	if e := m.Recover(ctx); e != nil {
		return failure(e)
	}
	if ctx.Err() != nil {
		return failure(code("COMPONENT_CANCELLED"))
	}
	exists := !os.IsNotExist(unwrapStat(m.journalPath()))
	var j journal
	var e error
	if exists {
		j, e = m.readJournal()
		if e != nil {
			return failure(e)
		}
		if j.CommittedTaskID == taskID {
			if j.CommittedCommandHash != commandHash(command) {
				return failure(code("COMPONENT_STATE_INVALID"))
			}
			observation := m.Observe(ctx)
			if e = maintenanceOutcome(command, observation); e != nil {
				return failure(e)
			}
			if command.Action == "logs" {
				if j.CommittedLogs == nil || j.CommittedLogs.Validate() != nil {
					return failure(code("COMPONENT_LOGS_UNAVAILABLE"))
				}
				return observation, j.CommittedLogs, nil
			}
			return observation, nil, nil
		}
		if command.ExpectedVersion != "" && command.ExpectedVersion != j.Ownership.Version {
			return failure(code("COMPONENT_VERSION_CONFLICT"))
		}
	}
	var logs *wire.ComponentLogs
	switch command.Action {
	case "install":
		if !exists {
			configuration := command.Config
			if configuration == nil {
				value := wire.DefaultComponentConfig()
				configuration = &value
			}
			e = m.installVersion(ctx, command.Version, configuration, download)
		} else if command.Version != j.Ownership.Version {
			e = code("COMPONENT_VERSION_CONFLICT")
		} else if command.Config != nil && *command.Config != appliedConfig(j.Ownership) {
			e = code("COMPONENT_CONFIG_INVALID")
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
			j.Phase = "removing"
			e = m.save(j)
			if e == nil {
				e = m.cleanup(ctx, j)
			}
		}
	case "observe":
	case "restart":
		if !exists {
			e = code("COMPONENT_STATE_INVALID")
		} else {
			progress.Step(ctx, wire.ProgressStepStart)
			var adapter maintenanceService
			adapter, e = m.adapter()
			if e == nil {
				e = adapter.Restart(ctx)
			}
		}
	case "configure", "upgrade", "rollback":
		if !exists {
			e = code("COMPONENT_STATE_INVALID")
		} else {
			e = m.update(ctx, j, command, taskID, download)
		}
	case "logs":
		if !exists {
			e = code("COMPONENT_LOGS_UNAVAILABLE")
		} else {
			var adapter maintenanceService
			adapter, e = m.adapter()
			if e == nil {
				var since time.Time
				since, e = m.ownedSince(j.Ownership)
				if e == nil {
					var value wire.ComponentLogs
					value, e = adapter.Journal(ctx, since)
					if e == nil {
						e = value.Validate()
						logs = &value
					}
				}
			}
		}
	default:
		e = code("AGENT_OPERATION_UNSUPPORTED")
	}
	if e != nil {
		progress.Error(ctx, "Node exporter %s failed: %s", command.Action, safeComponentError(e).Error())
		return failure(e)
	}
	progress.Step(ctx, wire.ProgressStepVerify)
	if ctx.Err() != nil {
		return failure(code("COMPONENT_CANCELLED"))
	}
	observation := m.Observe(ctx)
	if e = maintenanceOutcome(command, observation); e != nil {
		return failure(e)
	}
	if !os.IsNotExist(unwrapStat(m.journalPath())) {
		j, e = m.readJournal()
		if e != nil {
			return failure(e)
		}
		j.CommittedTaskID = taskID
		j.CommittedCommandHash = commandHash(command)
		if command.Action == "logs" {
			j.CommittedLogs = logs
		}
		if e = m.save(j); e != nil {
			return failure(e)
		}
	}
	return observation, logs, nil
}
func maintenanceOutcome(command wire.ComponentCommand, o wire.ComponentObservation) error {
	if e := o.ValidateMaintenance(); e != nil {
		return code("COMPONENT_STATE_INVALID")
	}
	if o.ErrorCode != "" {
		return code(o.ErrorCode)
	}
	switch command.Action {
	case "install", "start", "restart", "configure", "upgrade", "rollback":
		if !o.Managed || o.State != "running" || o.Metrics == nil || o.Version != command.Version {
			return code("COMPONENT_SERVICE_FAILED")
		}
		actual := wire.DefaultComponentConfig()
		if o.Config != nil {
			actual = *o.Config
		}
		if command.Config != nil && actual != *command.Config {
			return code("COMPONENT_CONFIG_INVALID")
		}
	case "stop":
		if !o.Managed || o.State != "stopped" {
			return code("COMPONENT_SERVICE_FAILED")
		}
	case "remove":
		if o.Managed || o.State != "not_installed" {
			return code("COMPONENT_STATE_INVALID")
		}
	case "logs":
		if !o.Managed {
			return code("COMPONENT_LOGS_UNAVAILABLE")
		}
	}
	return nil
}
func (m *Manager) prepareUnit(ctx context.Context, j *journal, target ownership) error {
	adapter, e := m.adapter()
	if e != nil {
		return e
	}
	if !m.validOwnership(target) {
		return code("COMPONENT_STATE_INVALID")
	}
	var random [16]byte
	if _, e = rand.Read(random[:]); e != nil {
		return code("COMPONENT_IO_FAILED")
	}
	j.UnitStaging = m.unit + ".staging-" + hex.EncodeToString(random[:]) + ".service"
	if e = m.save(*j); e != nil {
		return e
	}
	if e = writeNew(j.UnitStaging, m.unitFor(target), 0644); e != nil {
		return code("COMPONENT_IO_FAILED")
	}
	return adapter.ValidateUnit(ctx, j.UnitStaging)
}
func (m *Manager) replaceUnit(ctx context.Context, j *journal, from, to ownership) error {
	if ctx.Err() != nil {
		return code("COMPONENT_CANCELLED")
	}
	info, e := os.Lstat(m.unit)
	if e != nil {
		return code("COMPONENT_CONFLICT")
	}
	hash, e := hashFile(m.unit)
	if e != nil || hash != from.UnitSHA256 {
		return code("COMPONENT_CONFLICT")
	}
	if e = m.prepareUnit(ctx, j, to); e != nil {
		return e
	}
	current, e := os.Lstat(m.unit)
	if e != nil || !os.SameFile(info, current) {
		return code("COMPONENT_CONFLICT")
	}
	hash, e = hashFile(m.unit)
	if e != nil || hash != from.UnitSHA256 {
		return code("COMPONENT_CONFLICT")
	}
	if ctx.Err() != nil {
		return code("COMPONENT_CANCELLED")
	}
	if e = os.Rename(j.UnitStaging, m.unit); e != nil {
		return code("COMPONENT_IO_FAILED")
	}
	if e = syncDirectory(filepath.Dir(m.unit)); e != nil {
		return code("COMPONENT_IO_FAILED")
	}
	j.UnitStaging = ""
	return m.save(*j)
}
func (m *Manager) update(ctx context.Context, j journal, command wire.ComponentCommand, taskID string, download func(context.Context, string) error) error {
	adapter, e := m.adapter()
	if e != nil {
		return e
	}
	if e = m.verify(j, false); e != nil {
		return e
	}
	actual, e := m.service.State(ctx)
	if e != nil || actual != "running" {
		return code("COMPONENT_STATE_INVALID")
	}
	if j.Previous != nil && m.verifyOwnership(*j.Previous, false, false) != nil {
		return code("COMPONENT_ROLLBACK_UNAVAILABLE")
	}
	current := j.Ownership
	candidate := current
	if command.Action == "rollback" {
		if j.Previous == nil || j.Previous.Version != command.Version {
			return code("COMPONENT_ROLLBACK_UNAVAILABLE")
		}
		candidate = *j.Previous
	} else {
		if command.Action == "upgrade" && !wire.IsForwardUpgrade(current.Version, command.Version) {
			return code("COMPONENT_UPGRADE_NOT_FORWARD")
		}
		candidate.Version = command.Version
		r, approved := wire.RecipeForVersion(candidate.Version)
		if !approved {
			return code("COMPONENT_VERSION_UNSUPPORTED")
		}
		candidate.ArchiveSHA256 = r.ArchiveSHA256
		configuration := appliedConfig(current)
		if command.Config != nil {
			configuration = *command.Config
		}
		candidate.Config = &configuration
		if candidate.Version != current.Version {
			candidate.Files = nil
			if j.Previous != nil && j.Previous.Version == candidate.Version {
				candidate.Files = j.Previous.Files
				candidate.ManifestSHA256 = j.Previous.ManifestSHA256
			}
		}
	}
	if candidate.InstalledAt.IsZero() {
		candidate.InstalledAt, e = m.ownedSince(current)
		if e != nil {
			return e
		}
	}
	candidate.UnitSHA256 = bytesHash(m.unitFor(candidate))
	j.Candidate = &candidate
	j.Phase = "updating"
	j.LastMaintenanceCause = ""
	j.CandidateCreated = false
	if candidate.Version != current.Version && len(candidate.Files) == 0 {
		if !os.IsNotExist(unwrapStat(m.finalFor(candidate.Version))) {
			return code("COMPONENT_CONFLICT")
		}
		available, e := diskAvailable(m.config.RootDirectory)
		if e != nil || available < 256*1024*1024 {
			return code("COMPONENT_DISK_SPACE_LOW")
		}
		stage, e := os.MkdirTemp(m.config.RootDirectory, ".node-exporter-stage-")
		if e != nil {
			return code("COMPONENT_IO_FAILED")
		}
		j.Staging = stage
		j.CandidateCreated = true
	}
	if e = m.save(j); e != nil {
		if j.Staging != "" {
			os.Remove(j.Staging)
		}
		return e
	}
	operation := func() error {
		if j.CandidateCreated {
			archive := filepath.Join(m.journalDirectory, ".node-exporter-download.tar.gz")
			if !os.IsNotExist(unwrapStat(archive)) {
				return code("COMPONENT_STATE_INVALID")
			}
			defer os.Remove(archive)
			if e := download(ctx, archive); e != nil {
				return safeComponentError(e)
			}
			files, e := extractArchiveVersion(archive, j.Staging, candidate.Version)
			if e != nil {
				return e
			}
			candidate.Files = files
			candidate.ManifestSHA256 = bytesHash(manifestBytes(candidate))
			j.Candidate = &candidate
			if e = m.save(j); e != nil {
				return e
			}
			encoded := manifestBytes(candidate)
			if e = writeNew(filepath.Join(j.Staging, ".owned.json"), encoded, 0600); e != nil {
				return code("COMPONENT_IO_FAILED")
			}
			if e = os.Chmod(j.Staging, 0755); e != nil {
				return code("COMPONENT_IO_FAILED")
			}
			if ctx.Err() != nil {
				return code("COMPONENT_CANCELLED")
			}
			if !os.IsNotExist(unwrapStat(m.finalFor(candidate.Version))) {
				return code("COMPONENT_CONFLICT")
			}
			if e = os.Rename(j.Staging, m.finalFor(candidate.Version)); e != nil {
				return code("COMPONENT_IO_FAILED")
			}
			if e = syncDirectory(m.config.RootDirectory); e != nil {
				return code("COMPONENT_IO_FAILED")
			}
			j.Staging = ""
			if e = m.save(j); e != nil {
				return e
			}
		}
		if e := m.verifyOwnership(candidate, false, false); e != nil {
			return e
		}
		j.Phase = "switching"
		if e := m.save(j); e != nil {
			return e
		}
		if e := m.replaceUnit(ctx, &j, current, candidate); e != nil {
			return e
		}
		if e := m.service.Reload(ctx); e != nil {
			return e
		}
		j.Phase = "health"
		if e := m.save(j); e != nil {
			return e
		}
		if ctx.Err() != nil {
			return code("COMPONENT_CANCELLED")
		}
		if e := adapter.Restart(ctx); e != nil {
			return e
		}
		if probe := m.ObserveInstalling(ctx, journal{Ownership: candidate}); probe.ErrorCode != "" {
			return code(probe.ErrorCode)
		}
		if ctx.Err() != nil {
			return code("COMPONENT_CANCELLED")
		}
		j.Ownership = candidate
		j.Previous = &current
		j.Candidate = nil
		j.CandidateCreated = false
		j.Phase = "installed"
		j.CommittedTaskID = taskID
		j.CommittedCommandHash = commandHash(command)
		j.LastMaintenanceError = ""
		j.LastMaintenanceCause = ""
		return m.save(j)
	}
	if e = operation(); e == nil {
		return nil
	}
	if ctx.Err() != nil {
		return code("COMPONENT_CANCELLED")
	}
	// Reload the durable transition; in-memory fields may have advanced past a failed fsync.
	durable, readError := m.readJournal()
	if readError != nil {
		return code("COMPONENT_ROLLBACK_FAILED")
	}
	if durable.Phase == "installed" && durable.CommittedTaskID == taskID && durable.CommittedCommandHash == commandHash(command) {
		if syncDirectory(m.journalDirectory) == nil && m.verify(durable, false) == nil {
			return nil
		}
		return code("COMPONENT_IO_FAILED")
	}
	durable.LastMaintenanceCause = safeComponentError(e).Error()
	if m.save(durable) != nil {
		return code("COMPONENT_ROLLBACK_FAILED")
	}
	if recoverError := m.recoverMaintenance(ctx, durable); recoverError != nil {
		return code("COMPONENT_ROLLBACK_FAILED")
	}
	return code("COMPONENT_UPDATE_ROLLED_BACK")
}
func (m *Manager) clearMaintenanceStaging(j *journal) error {
	if j.UnitStaging != "" && !os.IsNotExist(unwrapStat(j.UnitStaging)) {
		info, e := os.Lstat(j.UnitStaging)
		if e != nil || !info.Mode().IsRegular() || !rootOwned(info) || info.Mode().Perm()&0022 != 0 {
			return code("COMPONENT_CONFLICT")
		}
		if os.Remove(j.UnitStaging) != nil {
			return code("COMPONENT_IO_FAILED")
		}
		if syncDirectory(filepath.Dir(m.unit)) != nil {
			return code("COMPONENT_IO_FAILED")
		}
	}
	j.UnitStaging = ""
	download := filepath.Join(m.journalDirectory, ".node-exporter-download.tar.gz")
	if !os.IsNotExist(unwrapStat(download)) {
		info, e := os.Lstat(download)
		if e != nil || !info.Mode().IsRegular() || !rootOwned(info) || info.Mode().Perm()&0077 != 0 {
			return code("COMPONENT_CONFLICT")
		}
		if os.Remove(download) != nil {
			return code("COMPONENT_IO_FAILED")
		}
	}
	if e := m.removeStaging(j.Staging); e != nil {
		return e
	}
	j.Staging = ""
	return nil
}
func (m *Manager) recoverMaintenance(ctx context.Context, j journal) error {
	if !maintenancePhase(j.Phase) || j.Candidate == nil {
		return code("COMPONENT_STATE_INVALID")
	}
	if e := m.verifyOwnership(j.Ownership, false, false); e != nil {
		return code("COMPONENT_ROLLBACK_UNAVAILABLE")
	}
	adapter, e := m.adapter()
	if e != nil {
		return e
	}
	if ctx.Err() != nil {
		return code("COMPONENT_CANCELLED")
	}
	hash, e := hashFile(m.unit)
	if e != nil {
		return code("COMPONENT_CONFLICT")
	}
	if hash == j.Candidate.UnitSHA256 && hash != j.Ownership.UnitSHA256 {
		if e = m.clearMaintenanceStaging(&j); e != nil {
			return e
		}
		j.Phase = "rolling_back"
		if e = m.save(j); e != nil {
			return e
		}
		if e = m.replaceUnit(ctx, &j, *j.Candidate, j.Ownership); e != nil {
			return e
		}
	} else if hash != j.Ownership.UnitSHA256 {
		return code("COMPONENT_CONFLICT")
	}
	if j.Phase != "updating" {
		// A restored unit file does not identify the manager's loaded unit or the
		// running process configuration. Same-version metrics cannot prove either.
		// Repeat both operations after an interrupted rollback, even when the
		// prior unit is already on disk; keep the transition journal until health.
		if e = m.service.Reload(ctx); e != nil {
			return e
		}
		if e = adapter.Restart(ctx); e != nil {
			return e
		}
	} else if actual, e := m.service.State(ctx); e != nil || actual != "running" {
		if e = adapter.Restart(ctx); e != nil {
			return e
		}
	} else if _, e = m.scrapeOwned(ctx, j.Ownership.Version); e != nil {
		if e = adapter.Restart(ctx); e != nil {
			return e
		}
	}
	if probe := m.ObserveInstalling(ctx, journal{Ownership: j.Ownership}); probe.ErrorCode != "" {
		return code("COMPONENT_ROLLBACK_FAILED")
	}
	if e = m.clearMaintenanceStaging(&j); e != nil {
		return e
	}
	if j.CandidateCreated && j.Candidate.Version != j.Ownership.Version && (j.Previous == nil || j.Previous.Version != j.Candidate.Version) && !os.IsNotExist(unwrapStat(m.finalFor(j.Candidate.Version))) {
		if e = m.verifyOwnership(*j.Candidate, false, false); e != nil {
			return e
		}
		if e = m.removeStaging(m.finalFor(j.Candidate.Version)); e != nil {
			return e
		}
	}
	j.Phase = "installed"
	j.Candidate = nil
	j.CandidateCreated = false
	j.LastMaintenanceError = "COMPONENT_UPDATE_ROLLED_BACK"
	if j.LastMaintenanceCause == "" {
		j.LastMaintenanceCause = "COMPONENT_INTERRUPTED"
	}
	return m.save(j)
}
