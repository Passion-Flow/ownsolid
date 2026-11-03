package scope

import (
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
)

type BackupStep struct {
	Phase string `json:"phase"`
	Role  string `json:"role,omitempty"`
}
type BackupVolume struct {
	Owner engine.Ownership `json:"owner"`
	UID   int              `json:"uid"`
	GID   int              `json:"gid"`
}
type BackupJob struct {
	Store    *engine.DatabaseTask       `json:"store,omitempty"`
	Postgres *engine.PostgresBackupTask `json:"postgres,omitempty"`
}
type BackupConfigurationFile struct {
	Role string    `json:"role"`
	File InputFile `json:"file"`
}
type BackupPlan struct {
	ReleaseViewBinding            *view.Configuration           `json:"release_view_binding,omitempty"`
	EffectiveControlPayloadSHA256 string                        `json:"effective_control_payload_sha256,omitempty"`
	RestoredAdmission             *RestoredApplicationAdmission `json:"restored_admission,omitempty"`
	FormatVersion                 int                           `json:"format_version"`
	AttemptID                     string                        `json:"attempt_id"`
	ScopeID                       string                        `json:"scope_id"`
	ReleaseID                     string                        `json:"release_id"`
	Profile                       string                        `json:"profile"`
	ConfigurationSHA256           string                        `json:"configuration_sha256"`
	ManifestSHA256                string                        `json:"manifest_sha256"`
	DescriptorSHA256              string                        `json:"descriptor_sha256"`
	RuntimeSHA256                 string                        `json:"runtime_sha256"`
	Request                       configuration.BackupRequest   `json:"request"`
	Initializer                   string                        `json:"initializer"`
	Volumes                       map[string]BackupVolume       `json:"volumes"`
	Jobs                          map[string]BackupJob          `json:"jobs"`
	ConfigurationFiles            []BackupConfigurationFile     `json:"configuration_files"`
	Steps                         []BackupStep                  `json:"steps"`
}

func (l *locked) makeBackupPlan(v verifiedRuntimeContext, r configuration.BackupRequest, id string) (BackupPlan, map[string][]engine.ConfigurationFile, error) {
	p := BackupPlan{FormatVersion: 1, AttemptID: id, ScopeID: l.marker.ScopeID, ReleaseID: l.marker.ReleaseID, Profile: l.marker.Profile, ConfigurationSHA256: l.marker.ConfigurationSHA256, ManifestSHA256: l.marker.ManifestSHA256, DescriptorSHA256: v.inputs.record.DescriptorSHA256, RuntimeSHA256: wire.Digest(encode(*v.creation.record)), Request: r, Initializer: v.inputs.descriptor.ImageID, Volumes: map[string]BackupVolume{}, Jobs: map[string]BackupJob{}, ConfigurationFiles: []BackupConfigurationFile{}}
	payloads := map[string][]engine.ConfigurationFile{}
	uid, gid, e := roleUser(v.model, "operator-config")
	if e != nil {
		return p, nil, e
	}
	pgUID, pgGID, e := roleUser(v.model, "postgres")
	if e != nil {
		return p, nil, e
	}
	roles := []string{}
	for role := range v.inputs.files {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	for _, role := range roles {
		for _, f := range v.inputs.files[role] {
			p.ConfigurationFiles = append(p.ConfigurationFiles, BackupConfigurationFile{Role: role, File: InputFile{Name: f.Name, Bytes: int64(len(f.Contents)), SHA256: wire.Digest(f.Contents)}})
		}
	}
	controlFiles := append([]engine.ConfigurationFile{}, v.inputs.files["operator-config"]...)
	seen := map[string]bool{}
	for _, f := range controlFiles {
		seen[f.Name] = true
	}
	for _, f := range v.inputs.files["control-config"] {
		if !seen[f.Name] {
			controlFiles = append(controlFiles, f)
			seen[f.Name] = true
		}
	}
	input := engine.Ownership{ScopeID: p.ScopeID, OperationID: id, Role: engine.StoreBackupInputRole + "-" + id}
	p.Volumes["control-input"] = BackupVolume{input, uid, gid}
	payloads["control-input"] = controlFiles
	peers := map[string]string{}
	for role, svc := range v.creation.record.Services {
		peers[role] = svc.ID
	}
	storeKinds := []string{engine.ModelFilesExport, engine.DatasetFilesExport, engine.TrainingFilesExport, engine.PreparationFilesExport}
	orderedJobs := []string{}
	for _, kind := range storeKinds {
		role, _ := engine.StoreBackupTaskContext(kind)
		source, declared := volumeState(l.events).roles[role]
		if !declared {
			continue
		}
		output := engine.Ownership{ScopeID: p.ScopeID, OperationID: id, Role: "backup-" + role + "-" + id}
		sourceOwner := l.volumeOwner(source)
		task := engine.DatabaseTask{Owner: engine.Ownership{ScopeID: p.ScopeID, OperationID: id, Role: "database-" + kind}, VolumeOwner: input, OutputOwner: &output, SourceOwner: &sourceOwner, Kind: kind, Image: p.Initializer, UID: uid, GID: gid, NetworkID: v.creation.record.NetworkID, Peers: peers, Reason: r.Reason}
		if e := l.bindTaskLocalization(&task, v.inputs, v.model); e != nil {
			return p, nil, e
		}
		p.Jobs[role] = BackupJob{Store: &task}
		orderedJobs = append(orderedJobs, role)
		p.Volumes[role+"-output"] = BackupVolume{output, uid, gid}
	}
	if len(orderedJobs) == 0 {
		return p, nil, failure("OFFLINE_SCOPE_BACKUP_STORES_REQUIRED")
	}
	dsn := ""
	for _, f := range controlFiles {
		if f.Name == "operator-dsn" {
			dsn = strings.TrimSpace(string(f.Contents))
		}
	}
	connection, e := url.Parse(dsn)
	if e != nil || connection.User == nil || connection.Scheme != "postgresql" && connection.Scheme != "postgres" || connection.RawQuery != "sslmode=disable" {
		return p, nil, failure("OFFLINE_SCOPE_BACKUP_CONNECTION_PROFILE_REQUIRED")
	}
	password, found := connection.User.Password()
	port, e := strconv.Atoi(connection.Port())
	if !found || e != nil {
		return p, nil, failure("OFFLINE_SCOPE_BACKUP_CONNECTION_PROFILE_REQUIRED")
	}
	pg := v.model.Services["postgres"]
	postgres := engine.PostgresBackupTask{Owner: engine.Ownership{ScopeID: p.ScopeID, OperationID: id, Role: "database-" + engine.PostgresBackupKind}, InputOwner: engine.Ownership{ScopeID: p.ScopeID, OperationID: id, Role: engine.PostgresBackupInputRole + "-" + id}, OutputOwner: engine.Ownership{ScopeID: p.ScopeID, OperationID: id, Role: engine.PostgresBackupOutputRole + "-" + id}, Image: pg.Image, UID: pgUID, GID: pgGID, NetworkID: v.creation.record.NetworkID, Peers: peers, Host: connection.Hostname(), Port: port, Database: strings.TrimPrefix(connection.Path, "/"), User: connection.User.Username()}
	file, e := engine.PostgresBackupPasswordFile(postgres, password)
	if e != nil {
		return p, nil, e
	}
	p.Jobs["postgres"] = BackupJob{Postgres: &postgres}
	orderedJobs = append(orderedJobs, "postgres")
	p.Volumes["postgres-input"] = BackupVolume{postgres.InputOwner, pgUID, pgGID}
	payloads["postgres-input"] = []engine.ConfigurationFile{file}
	p.Volumes["postgres-output"] = BackupVolume{postgres.OutputOwner, pgUID, pgGID}
	if e := l.bindCumulativeBackupPlan(&p); e != nil {
		return p, nil, e
	}
	if e := l.bindBackupReleasePolicy(&p, v.inputs.files); e != nil {
		return p, nil, e
	}
	p.Steps = append(p.Steps, BackupStep{Phase: "reserve"})
	volumeKeys := []string{}
	for key := range p.Volumes {
		volumeKeys = append(volumeKeys, key)
	}
	sort.Strings(volumeKeys)
	for _, key := range volumeKeys {
		for _, phase := range []string{"volume-create", "utility-create", "payload-write", "utility-remove"} {
			p.Steps = append(p.Steps, BackupStep{phase, key})
		}
	}
	for _, key := range orderedJobs {
		for _, phase := range []string{"task-create", "task-execute", "archive-publish", "task-remove"} {
			p.Steps = append(p.Steps, BackupStep{phase, key})
		}
	}
	for _, key := range volumeKeys {
		p.Steps = append(p.Steps, BackupStep{"volume-remove", key})
	}
	p.Steps = append(p.Steps, BackupStep{Phase: "manifest-publish"}, BackupStep{Phase: "complete"})
	if backupHostEntries(p)+postgresBackupArchiveEntries+2*(len(p.Jobs)-1) > r.MaximumEntries {
		return p, nil, failure("OFFLINE_SCOPE_BACKUP_ENTRY_LIMIT")
	}
	return p, payloads, nil
}
