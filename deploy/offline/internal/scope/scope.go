// Package scope binds an isolated installation to protected identity and durable effects.
package scope

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const markerName = ".projectone-installation-test-scope.json"
const lockName = ".scope.lock"
const journalName = ".scope.journal.jsonl"
const headName = ".scope.journal-head.json"
const pendingHeadName = ".scope.journal-head.pending"
const purpose = "isolated-validation"
const maximumEventBytes = 4096
const scopeFormatVersion = 1

var identityPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var codePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,95}$`)

type Marker struct {
	FormatVersion       int    `json:"format_version"`
	ScopeID             string `json:"scope_id"`
	Purpose             string `json:"purpose"`
	ConfigurationSHA256 string `json:"configuration_sha256"`
	ArchiveSHA256       string `json:"archive_sha256"`
	ManifestSHA256      string `json:"manifest_sha256"`
	Profile             string `json:"profile"`
	ReleaseID           string `json:"release_id"`
	CreatedAt           string `json:"created_at"`
	OwnerUID            int    `json:"owner_uid"`
}
type Event struct {
	CumulativeUpgrade     *CumulativeUpgradeRecord     `json:"cumulative_upgrade,omitempty"`
	JournalCapacity       *JournalCapacityRecord       `json:"journal_capacity,omitempty"`
	RestoredCurrentStores *RestoredCurrentStoresRecord `json:"restored_current_stores,omitempty"`
	RestoredLiveDatabase  *RestoredLiveDatabaseRecord  `json:"restored_live_database,omitempty"`
	ControlReplacement    *ControlReplacementRecord    `json:"control_replacement,omitempty"`
	TLSProjection         *TLSProjectionRecord         `json:"tls_projection,omitempty"`
	RestoreStores         *RestoreStoresRecord         `json:"restore_stores,omitempty"`
	RestoreDatabase       *RestoreDatabaseRecord       `json:"restore_database,omitempty"`
	RestoreSQL            *RestoreSQLRecord            `json:"restore_sql,omitempty"`
	Restore               *RestoreRecord               `json:"restore,omitempty"`
	Backup                *BackupRecord                `json:"backup,omitempty"`
	FormatVersion         int                          `json:"format_version"`
	Sequence              int                          `json:"sequence"`
	PreviousSHA256        string                       `json:"previous_event_sha256"`
	ScopeID               string                       `json:"scope_id"`
	ConfigurationSHA256   string                       `json:"configuration_sha256"`
	ArchiveSHA256         string                       `json:"archive_sha256"`
	ManifestSHA256        string                       `json:"manifest_sha256"`
	OperationID           string                       `json:"operation_id"`
	Action                string                       `json:"action"`
	Outcome               string                       `json:"outcome"`
	Code                  string                       `json:"code"`
	Time                  string                       `json:"time"`
	Engine                *engine.Observation          `json:"engine,omitempty"`
	Image                 *ImageRecord                 `json:"image,omitempty"`
	ReferenceOperationID  string                       `json:"reference_operation_id,omitempty"`
	Inputs                *InputsRecord                `json:"inputs,omitempty"`
	Volume                *VolumeRecord                `json:"volume,omitempty"`
	Runtime               *RuntimeRecord               `json:"runtime,omitempty"`
	Process               *ProcessRecord               `json:"process,omitempty"`
	ModelTask             *ModelTaskRecord             `json:"model_task,omitempty"`
	ModelTaskAction       *ModelTaskActionRecord       `json:"model_task_action,omitempty"`
	DatabaseTask          *DatabaseTaskRecord          `json:"database_task,omitempty"`
	DatabaseTaskAction    *DatabaseTaskActionRecord    `json:"database_task_action,omitempty"`
}
type Status struct {
	CumulativeUpgrade          *CumulativeUpgradeResult     `json:"cumulative_upgrade,omitempty"`
	JournalCapacity            *JournalCapacityStatus       `json:"journal_capacity,omitempty"`
	Process                    *RecordedProcessStatus       `json:"process,omitempty"`
	RestoredApplication        *RecordedProcessStatus       `json:"restored_application,omitempty"`
	RestoredCurrentStores      *RestoredCurrentStoresResult `json:"restored_current_stores,omitempty"`
	RestoredLiveDatabase       *RestoredLiveDatabaseResult  `json:"restored_live_database,omitempty"`
	ControlReplacement         *ControlReplacementResult    `json:"control_replacement,omitempty"`
	TLSProjection              *TLSProjectionResult         `json:"tls_projection,omitempty"`
	RestoreStores              *RestoreStoresResult         `json:"restore_stores,omitempty"`
	RestoreDatabase            *RestoreDatabaseResult       `json:"restore_database,omitempty"`
	RestoreSQL                 *RestoreSQLResult            `json:"restore_sql,omitempty"`
	Restore                    *RestoreReservationResult    `json:"restore,omitempty"`
	Backup                     *BackupResult                `json:"backup,omitempty"`
	Stores                     map[string]StoreStatus       `json:"stores,omitempty"`
	BootstrapQualified         bool                         `json:"bootstrap_qualified,omitempty"`
	BootstrapCredentialFile    string                       `json:"bootstrap_credential_file,omitempty"`
	Status                     string                       `json:"status"`
	ScopeID                    string                       `json:"scope_id"`
	Purpose                    string                       `json:"purpose"`
	ConfigurationSHA256        string                       `json:"configuration_sha256"`
	ArchiveSHA256              string                       `json:"archive_sha256"`
	ManifestSHA256             string                       `json:"manifest_sha256"`
	Profile                    string                       `json:"profile"`
	ReleaseID                  string                       `json:"release_id"`
	Events                     int                          `json:"events"`
	Qualification              string                       `json:"qualification"`
	ImagesRecorded             int                          `json:"images_recorded,omitempty"`
	ImagesPending              int                          `json:"images_pending,omitempty"`
	VolumesPrepared            int                          `json:"volumes_prepared,omitempty"`
	VolumesPending             int                          `json:"volumes_pending,omitempty"`
	ModelTaskID                string                       `json:"model_task_id,omitempty"`
	ModelTaskPending           bool                         `json:"model_task_pending,omitempty"`
	ModelDirectoryInitialized  bool                         `json:"model_directory_initialized,omitempty"`
	ModelTaskRemoved           bool                         `json:"model_task_removed,omitempty"`
	DatabaseTaskID             string                       `json:"database_task_id,omitempty"`
	DatabaseTaskPending        bool                         `json:"database_task_pending,omitempty"`
	MigrationProcessSucceeded  bool                         `json:"migration_process_succeeded,omitempty"`
	DatabaseTaskRemoved        bool                         `json:"database_task_removed,omitempty"`
	DatabaseTaskKind           string                       `json:"database_task_kind,omitempty"`
	SchemaProcessSucceeded     bool                         `json:"schema_process_succeeded,omitempty"`
	SchemaQualified            bool                         `json:"schema_qualified,omitempty"`
	AccessPreparationSucceeded bool                         `json:"access_preparation_succeeded,omitempty"`
	AccessCheckSucceeded       bool                         `json:"access_check_succeeded,omitempty"`
	RuntimeAccessQualified     bool                         `json:"runtime_access_qualified,omitempty"`
}
type journalHead struct {
	FormatVersion       int    `json:"format_version"`
	ScopeID             string `json:"scope_id"`
	ConfigurationSHA256 string `json:"configuration_sha256"`
	Sequence            int    `json:"sequence"`
	EventSHA256         string `json:"event_sha256"`
	JournalBytes        int64  `json:"journal_bytes"`
}
type locked struct {
	journalBytes int64
	root         *os.Root
	lock         *os.File
	marker       Marker
	events       []Event
	lastSHA      string
	lastHead     []byte
	config       configuration.Config
}

func failure(code string) error { return errors.New(code) }
func encode(v any) []byte       { raw, _ := json.Marshal(v); return append(raw, '\n') }
func nonce() (string, error) {
	raw := make([]byte, 16)
	if _, e := rand.Read(raw); e != nil {
		return "", failure("OFFLINE_SCOPE_RANDOMNESS_UNAVAILABLE")
	}
	return hex.EncodeToString(raw), nil
}
func contained(root, name string) bool {
	relative, e := filepath.Rel(root, name)
	return e == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
func canonical(name string) bool {
	resolved, e := filepath.EvalSymlinks(name)
	return e == nil && filepath.IsAbs(name) && filepath.Clean(name) == name && resolved == name
}
func owned(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Geteuid()
}
func mounted(name string) bool {
	if name == "/" || !canonical(name) {
		return false
	}
	var a, b syscall.Stat_t
	return syscall.Stat(name, &a) == nil && syscall.Stat(filepath.Dir(name), &b) == nil && a.Dev != b.Dev
}
func privateDirectory(name string) bool {
	info, e := os.Lstat(name)
	return e == nil && canonical(name) && owned(info) && info.Mode() == os.ModeDir|0700
}
func inputs(c configuration.Config) error {
	if !mounted(c.Verification.MountRoot) || !filepath.IsAbs(c.ScopeRoot) || filepath.Clean(c.ScopeRoot) != c.ScopeRoot || !contained(c.Verification.MountRoot, c.ScopeRoot) || !privateDirectory(filepath.Dir(c.ScopeRoot)) {
		return failure("OFFLINE_SCOPE_NEW_PRIVATE_MOUNTED_ROOT_REQUIRED")
	}
	if c.DestinationRoot != "" && (c.ScopeRoot == c.DestinationRoot || contained(c.DestinationRoot, c.ScopeRoot) || contained(c.ScopeRoot, c.DestinationRoot)) {
		return failure("OFFLINE_SCOPE_PREPARED_ROOT_OVERLAP_REFUSED")
	}
	return nil
}
func writeNew(root *os.Root, name string, raw []byte) error {
	f, e := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return failure("OFFLINE_SCOPE_FILE_OCCUPIED")
	}
	n, we := f.Write(raw)
	se := f.Sync()
	ce := f.Close()
	if we != nil || se != nil || ce != nil || n != len(raw) {
		return failure("OFFLINE_SCOPE_FILE_DURABILITY_FAILED")
	}
	return nil
}
func (l *locked) close() {
	if l.lock != nil {
		_ = syscall.Flock(int(l.lock.Fd()), syscall.LOCK_UN)
		l.lock.Close()
	}
	if l.root != nil {
		l.root.Close()
	}
}
func open(c configuration.Config, exclusive bool) (*locked, error) {
	if c.BoundScopeID != "" && !identityPattern.MatchString(c.BoundScopeID) {
		return nil, failure("OFFLINE_SCOPE_BINDING_INVALID")
	}
	if e := inputs(c); e != nil {
		return nil, e
	}
	if !privateDirectory(c.ScopeRoot) {
		return nil, failure("OFFLINE_SCOPE_PRIVATE_ROOT_REQUIRED")
	}
	root, e := openPrivateDirectory(c.ScopeRoot)
	if e != nil {
		return nil, failure("OFFLINE_SCOPE_ROOT_UNAVAILABLE")
	}
	l := &locked{root: root, config: c}
	ok := false
	defer func() {
		if !ok {
			l.close()
		}
	}()
	lockRaw, e := regular(root, lockName, 128)
	if e != nil {
		return nil, e
	}
	flags := os.O_RDONLY
	if exclusive {
		flags = os.O_RDWR
	}
	l.lock, e = root.OpenFile(lockName, flags, 0)
	if e != nil {
		return nil, failure("OFFLINE_SCOPE_LOCK_UNAVAILABLE")
	}
	mode := syscall.LOCK_SH | syscall.LOCK_NB
	if exclusive {
		mode = syscall.LOCK_EX | syscall.LOCK_NB
	}
	if syscall.Flock(int(l.lock.Fd()), mode) != nil {
		return nil, failure("OFFLINE_SCOPE_BUSY")
	}
	raw, e := regular(root, markerName, 1<<20)
	if e != nil || wire.StrictJSON(raw, &l.marker) != nil {
		return nil, failure("OFFLINE_SCOPE_MARKER_INVALID")
	}
	m := l.marker
	if c.BoundScopeID != "" && m.ScopeID != c.BoundScopeID {
		return nil, failure("OFFLINE_SCOPE_BINDING_MISMATCH")
	}
	if m.FormatVersion != scopeFormatVersion || !identityPattern.MatchString(m.ScopeID) || m.Purpose != purpose || m.OwnerUID != os.Geteuid() || m.ConfigurationSHA256 != c.Fingerprint() || m.ArchiveSHA256 != c.ExpectedArchiveSHA256 || m.ManifestSHA256 != c.ExpectedManifestSHA256 || string(lockRaw) != m.ScopeID+"\n" {
		return nil, failure("OFFLINE_SCOPE_CONTEXT_MISMATCH")
	}
	if _, e = time.Parse(time.RFC3339Nano, m.CreatedAt); e != nil {
		return nil, failure("OFFLINE_SCOPE_MARKER_INVALID")
	}
	journal, e := regular(root, journalName, configuration.MaximumJournalBytes)
	if e != nil {
		return nil, e
	}
	l.events, l.lastSHA, e = validateJournal(journal, m)
	if e != nil {
		return nil, e
	}
	limit, capacityError := validateJournalCapacityPrefixes(journal, l.events, c.MaximumJournalBytes)
	if capacityError != nil {
		return nil, capacityError
	}
	if int64(len(journal)) > limit {
		return nil, failure("OFFLINE_SCOPE_JOURNAL_CAPACITY_REQUIRED")
	}
	l.journalBytes = int64(len(journal))
	l.lastHead, e = regular(root, headName, maximumEventBytes)
	if e != nil {
		return nil, failure("OFFLINE_SCOPE_JOURNAL_HEAD_UNCERTAIN")
	}
	var head journalHead
	if wire.StrictJSON(l.lastHead, &head) != nil || head.FormatVersion != scopeFormatVersion || head.ScopeID != m.ScopeID || head.ConfigurationSHA256 != m.ConfigurationSHA256 || head.Sequence != len(l.events) || head.EventSHA256 != l.lastSHA || head.JournalBytes != int64(len(journal)) {
		return nil, failure("OFFLINE_SCOPE_JOURNAL_HEAD_UNCERTAIN")
	}
	folder, e := root.Open(".")
	if e != nil {
		return nil, failure("OFFLINE_SCOPE_ROOT_UNAVAILABLE")
	}
	defer folder.Close()
	allowed := map[string]bool{restoreStoresPlanFile: true, restorePlanFile: true, bootstrapCopyName: true, markerName: true, lockName: true, journalName: true, headName: true, "release": true, "release.journal.jsonl": true, inputsDirectory: true, "data": true}
	privateDirectories := map[string]bool{"release": true, inputsDirectory: true, "data": true}
	for _, event := range l.events {
		if event.CumulativeUpgrade != nil {
			allowed[cumulativeUpgradePlanFile] = true
		}
		if event.RestoredCurrentStores != nil {
			allowed[currentStoresPlanFile(event.RestoredCurrentStores.GenerationID)] = true
		}
		if event.RestoredLiveDatabase != nil {
			allowed[liveDatabasePlanFile(event.RestoredLiveDatabase.GenerationID)] = true
		}
		if event.ControlReplacement != nil {
			allowed[controlReplacementPlanName(l.events, event.ControlReplacement.GenerationID)] = true
		}
		if event.TLSProjection != nil {
			allowed[tlsProjectionPlanFile(event.TLSProjection.GenerationID)] = true
			if event.TLSProjection.PayloadCustody {
				name := generationPayloadDirectory(event.TLSProjection.GenerationID)
				allowed[name], privateDirectories[name] = true, true
			}
		}
	}
	for {
		entries, e := folder.ReadDir(16)
		if e != nil && e != io.EOF {
			return nil, failure("OFFLINE_SCOPE_ROOT_UNAVAILABLE")
		}
		for _, entry := range entries {
			if entry.Name() == pendingHeadName {
				return nil, failure("OFFLINE_SCOPE_JOURNAL_HEAD_UNCERTAIN")
			}
			if !allowed[entry.Name()] {
				return nil, failure("OFFLINE_SCOPE_UNKNOWN_CONTENT_PRESERVED")
			}
			info, e := root.Lstat(entry.Name())
			if e != nil || !owned(info) {
				return nil, failure("OFFLINE_SCOPE_ROOT_CONTENT_UNSAFE")
			}
			if privateDirectories[entry.Name()] {
				if info.Mode() != os.ModeDir|0700 {
					return nil, failure("OFFLINE_SCOPE_ROOT_CONTENT_UNSAFE")
				}
			} else {
				st, ok := info.Sys().(*syscall.Stat_t)
				if info.Mode() != 0600 || !ok || st.Nlink != 1 {
					return nil, failure("OFFLINE_SCOPE_ROOT_CONTENT_UNSAFE")
				}
			}
		}
		if e == io.EOF {
			break
		}
	}
	if _, de := root.Lstat("data"); de == nil {
		if pe := l.verifyReleaseViewInventory(); pe != nil {
			return nil, pe
		}
	} else if !os.IsNotExist(de) {
		return nil, de
	}
	if e = l.verifyBootstrapCopy(); e != nil {
		return nil, e
	}
	if e = l.verifyRestorePlan(); e != nil {
		return nil, e
	}
	if e = l.verifyRecordedRestoreDatabase(); e != nil {
		return nil, e
	}
	if e = l.verifyRestoreStoresPlan(); e != nil {
		return nil, e
	}
	if e = l.verifyControlReplacementPlan(); e != nil {
		return nil, e
	}
	if e = l.verifyTLSProjectionPlans(); e != nil {
		return nil, e
	}
	if e = l.verifyLiveDatabasePlans(); e != nil {
		return nil, e
	}
	if e = l.verifyCurrentStoresPlans(); e != nil {
		return nil, e
	}
	if e = l.verifyApplicationAdmissions(); e != nil {
		return nil, e
	}
	if e = l.verifyCumulativeUpgradePlan(); e != nil {
		return nil, e
	}
	if e = l.verifyBackupAdmissions(); e != nil {
		return nil, e
	}
	ok = true
	return l, nil
}
func validateJournal(raw []byte, m Marker) ([]Event, string, error) {
	if !bytes.HasSuffix(raw, []byte("\n")) {
		return nil, "", failure("OFFLINE_SCOPE_JOURNAL_TRUNCATED")
	}
	lines := bytes.Split(bytes.TrimSuffix(raw, []byte("\n")), []byte("\n"))
	events := []Event{}
	previous := ""
	pending := ""
	pendingAction := ""
	for i, line := range lines {
		var event Event
		if len(line) > maximumEventBytes || wire.StrictJSON(line, &event) != nil || event.FormatVersion != scopeFormatVersion || event.Sequence != i+1 || event.PreviousSHA256 != previous || event.ScopeID != m.ScopeID || event.ConfigurationSHA256 != m.ConfigurationSHA256 || event.ArchiveSHA256 != m.ArchiveSHA256 || event.ManifestSHA256 != m.ManifestSHA256 || !identityPattern.MatchString(event.OperationID) {
			return nil, "", failure("OFFLINE_SCOPE_JOURNAL_CONTEXT_OR_CHAIN_INVALID")
		}
		if _, e := time.Parse(time.RFC3339Nano, event.Time); e != nil {
			return nil, "", failure("OFFLINE_SCOPE_JOURNAL_TIME_INVALID")
		}
		if i >= 3 {
			if e := validateImageEvent(events, event); e != nil {
				return nil, "", e
			}
		} else if event.CumulativeUpgrade != nil || event.JournalCapacity != nil || event.RestoredCurrentStores != nil || event.RestoredLiveDatabase != nil || event.ControlReplacement != nil || event.TLSProjection != nil || event.RestoreStores != nil || event.RestoreDatabase != nil || event.RestoreSQL != nil || event.Restore != nil || event.Backup != nil || event.Engine != nil || event.Image != nil || event.ReferenceOperationID != "" || event.Inputs != nil || event.Volume != nil || event.Runtime != nil || event.Process != nil || event.ModelTask != nil || event.ModelTaskAction != nil || event.DatabaseTask != nil || event.DatabaseTaskAction != nil {
			return nil, "", failure("OFFLINE_SCOPE_JOURNAL_TRANSITION_INVALID")
		} else if i == 0 {
			if event.Action != "scope_create" || event.Outcome != "completed" || event.Code != "" {
				return nil, "", failure("OFFLINE_SCOPE_JOURNAL_GENESIS_INVALID")
			}
		} else {
			if event.Action != "release_prepare" {
				return nil, "", failure("OFFLINE_SCOPE_JOURNAL_ACTION_UNSUPPORTED")
			}
			switch event.Outcome {
			case "started":
				if pending != "" || event.Code != "" {
					return nil, "", failure("OFFLINE_SCOPE_JOURNAL_TRANSITION_INVALID")
				}
				pending = event.OperationID
				pendingAction = event.Action
			case "completed", "failed", "uncertain":
				if pending != event.OperationID || pendingAction != event.Action || event.Outcome == "completed" && event.Code != "" || event.Outcome != "completed" && !codePattern.MatchString(event.Code) {
					return nil, "", failure("OFFLINE_SCOPE_JOURNAL_TRANSITION_INVALID")
				}
				pending = ""
				pendingAction = ""
			default:
				return nil, "", failure("OFFLINE_SCOPE_JOURNAL_TRANSITION_INVALID")
			}
		}
		previous = wire.Digest(line)
		events = append(events, event)
	}
	return events, previous, nil
}
func (l *locked) append(action, outcome, operationID, code string) error {
	event := Event{FormatVersion: scopeFormatVersion, Sequence: len(l.events) + 1, PreviousSHA256: l.lastSHA, ScopeID: l.marker.ScopeID, ConfigurationSHA256: l.marker.ConfigurationSHA256, ArchiveSHA256: l.marker.ArchiveSHA256, ManifestSHA256: l.marker.ManifestSHA256, OperationID: operationID, Action: action, Outcome: outcome, Code: code, Time: time.Now().UTC().Format(time.RFC3339Nano)}
	return l.appendEvent(event)
}
func (l *locked) appendEvent(event Event) error {
	event.FormatVersion = scopeFormatVersion
	event.Sequence = len(l.events) + 1
	event.PreviousSHA256 = l.lastSHA
	event.ScopeID = l.marker.ScopeID
	event.ConfigurationSHA256 = l.marker.ConfigurationSHA256
	event.ArchiveSHA256 = l.marker.ArchiveSHA256
	event.ManifestSHA256 = l.marker.ManifestSHA256
	event.Time = time.Now().UTC().Format(time.RFC3339Nano)
	if len(l.events) < 3 && (event.CumulativeUpgrade != nil || event.JournalCapacity != nil || event.RestoredCurrentStores != nil || event.RestoredLiveDatabase != nil || event.ControlReplacement != nil || event.TLSProjection != nil || event.RestoreStores != nil || event.RestoreDatabase != nil || event.RestoreSQL != nil || event.Restore != nil || event.Backup != nil || event.ModelTask != nil || event.ModelTaskAction != nil || event.DatabaseTask != nil || event.DatabaseTaskAction != nil) {
		return failure("OFFLINE_SCOPE_MODEL_TASK_TRANSITION_INVALID")
	}
	if len(l.events) >= 3 {
		if e := validateImageEvent(l.events, event); e != nil {
			return e
		}
	}
	limit, capacityError := effectiveJournalCapacity(l.events, l.config.MaximumJournalBytes)
	if capacityError != nil {
		return capacityError
	}
	if event.JournalCapacity != nil {
		if event.JournalCapacity.Request.ExpectedCurrentBytes != limit {
			return failure("OFFLINE_SCOPE_JOURNAL_EXPECTED_CAPACITY_CHANGED")
		}
		limit = event.JournalCapacity.Request.MaximumJournalBytes
	}
	raw := encode(event)
	before, e := l.root.Lstat(journalName)
	if e != nil || int64(len(raw))+before.Size() > limit || len(raw) > maximumEventBytes {
		return failure("OFFLINE_SCOPE_JOURNAL_CAPACITY_REQUIRED")
	}
	if e = l.beginReleaseView(); e != nil {
		return failure("OFFLINE_SCOPE_RELEASE_VIEW_RECONCILE_REQUIRED")
	}
	f, e := l.root.OpenFile(journalName, os.O_WRONLY|os.O_APPEND, 0)
	if e != nil {
		return failure("OFFLINE_SCOPE_JOURNAL_UNAVAILABLE")
	}
	n, we := f.Write(raw)
	se := f.Sync()
	ce := f.Close()
	if we != nil || se != nil || ce != nil || n != len(raw) {
		return failure("OFFLINE_SCOPE_JOURNAL_DURABILITY_UNCERTAIN")
	}
	if len(l.lastHead) > 0 {
		stored, e := regular(l.root, headName, maximumEventBytes)
		if e != nil || !bytes.Equal(stored, l.lastHead) {
			return failure("OFFLINE_SCOPE_JOURNAL_HEAD_UNCERTAIN")
		}
	} else {
		if _, e = l.root.Lstat(headName); !os.IsNotExist(e) {
			return failure("OFFLINE_SCOPE_JOURNAL_HEAD_UNCERTAIN")
		}
	}
	nextSHA := wire.Digest(bytes.TrimSuffix(raw, []byte("\n")))
	head := encode(journalHead{FormatVersion: scopeFormatVersion, ScopeID: l.marker.ScopeID, ConfigurationSHA256: l.marker.ConfigurationSHA256, Sequence: event.Sequence, EventSHA256: nextSHA, JournalBytes: before.Size() + int64(len(raw))})
	if e = writeNew(l.root, pendingHeadName, head); e != nil {
		return failure("OFFLINE_SCOPE_JOURNAL_HEAD_UNCERTAIN")
	}
	if e = l.root.Rename(pendingHeadName, headName); e != nil {
		return failure("OFFLINE_SCOPE_JOURNAL_HEAD_UNCERTAIN")
	}
	folder, e := l.root.Open(".")
	if e != nil {
		return failure("OFFLINE_SCOPE_JOURNAL_HEAD_UNCERTAIN")
	}
	se = folder.Sync()
	folder.Close()
	if se != nil {
		return failure("OFFLINE_SCOPE_JOURNAL_HEAD_UNCERTAIN")
	}
	l.events = append(l.events, event)
	l.journalBytes = before.Size() + int64(len(raw))
	l.lastSHA = nextSHA
	l.lastHead = head
	if e = l.publishReleaseView(); e != nil {
		return failure("OFFLINE_SCOPE_RELEASE_VIEW_DURABILITY_UNCERTAIN")
	}
	return nil
}
func (l *locked) status() Status {
	m := l.marker
	last := l.events[len(l.events)-1]
	phase := "isolated_scope_action_uncertain"
	if last.Action == "scope_create" {
		phase = "isolated_scope_created"
	} else if last.Outcome == "completed" {
		phase = "isolated_scope_prepared"
	} else if last.Outcome == "failed" {
		phase = "isolated_scope_failed"
	}
	qualification := "verified_file_preparation_only_not_application_installation_or_target"
	if m.Profile == wire.FixtureProfile {
		qualification = "synthetic_isolated_scope_preparation_only_never_application_installation_or_target"
	}
	state := imageState(l.events)
	if len(l.events) > 3 {
		phase = "isolated_scope_images_recorded"
		qualification = "recorded_image_observations_only_not_live_engine_application_or_target"
		if len(state.pending) > 0 {
			phase = "isolated_scope_image_action_uncertain"
		}
		if len(state.completed) == 0 && len(state.pending) == 0 {
			phase = "isolated_scope_prepared_engine_bound"
		}
	}
	if record, outcome, _ := inputsState(l.events); record != nil {
		phase = "isolated_scope_storage_inputs_uncertain"
		if outcome == "completed" {
			phase = "isolated_scope_storage_inputs_prepared"
		}
		qualification = "storage_input_snapshot_only_not_volume_namespace_application_or_target"
	}
	volumes := volumeState(l.events)
	prepared := 0
	for _, record := range volumes.roles {
		if record.Phase == phasePrepared {
			prepared++
		}
	}
	if len(volumes.roles) > 0 {
		phase = "isolated_scope_storage_payload_progress"
		qualification = "recorded_role_payloads_only_not_business_namespaces_database_application_or_target"
		if len(volumes.pending) > 0 {
			phase = "isolated_scope_storage_action_uncertain"
		}
	}
	journal := l.journalStatus()
	return Status{JournalCapacity: &journal, Status: phase, ScopeID: m.ScopeID, Purpose: m.Purpose, ConfigurationSHA256: m.ConfigurationSHA256, ArchiveSHA256: m.ArchiveSHA256, ManifestSHA256: m.ManifestSHA256, Profile: m.Profile, ReleaseID: m.ReleaseID, Events: len(l.events), Qualification: qualification, ImagesRecorded: len(state.completed), ImagesPending: len(state.pending), VolumesPrepared: prepared, VolumesPending: len(volumes.pending)}
}
func Read(ctx context.Context, c configuration.Config) (Status, error) {
	if ctx.Err() != nil {
		return Status{}, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	l, e := open(c, false)
	if e != nil {
		return Status{}, e
	}
	defer l.close()
	s := l.status()
	if backupState(l.events).record != nil {
		b := l.backupResult()
		s.Backup = &b
	}
	s.Stores = storeStatuses(l.events)
	if len(l.events) >= 3 && l.events[2].Outcome == "completed" {
		r, e := bundle.VerifyPrepared(ctx, c.Verification, filepath.Join(c.ScopeRoot, "release"), c.ExpectedArchiveSHA256, c.ExpectedManifestSHA256)
		if e != nil {
			return Status{}, e
		}
		if r.Profile != l.marker.Profile || r.ReleaseID != l.marker.ReleaseID {
			return Status{}, failure("OFFLINE_SCOPE_RELEASE_CONTEXT_MISMATCH")
		}
		if e = validateImageAssets(l.events, r.Manifest); e != nil {
			return Status{}, e
		}
		if record, outcome, _ := inputsState(l.events); record != nil && outcome == "completed" {
			p, se := l.readInputs(ctx, *record)
			if se != nil {
				return Status{}, se
			}
			if e = validateVolumeInputs(volumeState(l.events), p); e != nil {
				return Status{}, e
			}
			if s.VolumesPrepared == len(p.descriptor.Roles) && s.VolumesPending == 0 {
				s.Status = "isolated_scope_storage_payloads_prepared"
			}
			task := modelTaskState(l.events)
			if task.record != nil {
				if !validModelTaskRecord(l.events, task.record, l.marker.ScopeID) || len(p.files["models"]) != 0 {
					return Status{}, failure("OFFLINE_SCOPE_MODEL_TASK_INPUT_MISMATCH")
				}
				s.ModelTaskID = task.record.ID
				s.ModelTaskPending = task.pending != ""
				s.Status = "isolated_scope_never_started_model_task_recorded"
				s.Qualification = "recorded_task_creation_only_not_current_process_initialized_storage_application_or_target"
				if task.pending != "" {
					s.Status = "isolated_scope_model_task_creation_uncertain"
				}
			}
			if e = validateModelTaskActionInputs(l.events, c); e != nil {
				return Status{}, e
			}
			actions := modelTaskActionState(l.events)
			if actions.record != nil {
				s.ModelTaskPending = actions.pending != ""
				s.Status = "isolated_scope_model_task_action_recorded"
				s.Qualification = "recorded_model_directory_initializer_action_only_not_database_application_or_target"
				if actions.initialized {
					s.Status = "isolated_scope_model_directory_initialized"
				}
				if actions.failed {
					s.Status = "isolated_scope_model_initializer_failed"
				}
				if actions.removed {
					s.Status = "isolated_scope_model_task_removed"
				}
				if actions.pending != "" {
					s.Status = "isolated_scope_model_task_action_uncertain"
				}
				s.ModelDirectoryInitialized = actions.initialized
				s.ModelTaskRemoved = actions.removed
			}
			runtime := runtimeState(l.events)
			if e = validateRuntimeInputs(runtime, p); e != nil {
				return Status{}, e
			}
			if runtime.record != nil {
				s.Status = "isolated_scope_runtime_action_uncertain"
				if runtime.completed {
					s.Status = "isolated_scope_runtime_resources_recorded"
				}
				s.Qualification = "recorded_resource_creation_only_not_current_process_health_application_or_target"
			}
			if e = validateProcessInputs(l.events, p, c); e != nil {
				return Status{}, e
			}
			process := processState(l.events)
			if process.record != nil {
				s.Status = "isolated_scope_process_outcome_recorded"
				if process.pending != "" {
					s.Status = "isolated_scope_process_action_uncertain"
				}
				s.Qualification = "historical_process_outcome_only_not_current_application_health_or_target"
			}
			if task := databaseTaskState(l.events); task.record != nil {
				if !validDatabaseTaskRecord(l.events, task.record, l.marker.ScopeID) {
					return Status{}, failure("OFFLINE_SCOPE_DATABASE_TASK_INPUT_MISMATCH")
				}
				s.DatabaseTaskID = task.record.ID
				s.DatabaseTaskKind = task.record.Task.Kind
				s.DatabaseTaskPending = task.pending != ""
				s.Status = "isolated_scope_never_started_database_task_recorded"
				s.Qualification = "recorded_database_task_creation_only_not_migrated_schema_current_process_application_or_target"
				if task.pending != "" {
					s.Status = "isolated_scope_database_task_creation_uncertain"
				}
				if e = validateDatabaseTaskActionInputs(l.events, c); e != nil {
					return Status{}, e
				}
				a := databaseTaskActionState(l.events)
				s.MigrationProcessSucceeded = task.record.Task.Kind == engine.DatabaseMigrate && a.succeeded
				if _, ok := migrationProof(l.events); ok {
					s.MigrationProcessSucceeded = true
				}
				s.SchemaProcessSucceeded = task.record.Task.Kind == engine.DatabaseSchemaCheck && a.succeeded
				if _, ok := schemaProof(l.events); ok {
					s.SchemaProcessSucceeded = true
				}
				s.SchemaQualified = schemaQualified(l.events)
				s.AccessPreparationSucceeded = task.record.Task.Kind == engine.DatabaseRuntimePrepare && a.succeeded
				if _, ok := accessPreparationProof(l.events); ok {
					s.AccessPreparationSucceeded = true
				}
				s.AccessCheckSucceeded = task.record.Task.Kind == engine.DatabaseRuntimeCheck && a.succeeded
				if _, ok := completedDatabaseTaskProof(l.events, engine.DatabaseRuntimeCheck); ok {
					s.AccessCheckSucceeded = true
				}
				s.RuntimeAccessQualified = runtimeAccessQualified(l.events)
				s.BootstrapQualified = bootstrapQualified(l.events)

				if bootstrapCopySHA(l.events) != "" {
					s.BootstrapCredentialFile = bootstrapCopyName
				}
				if a.record != nil {
					s.DatabaseTaskRemoved = a.removed
					s.DatabaseTaskPending = a.pending != ""
					s.Status = "isolated_scope_database_task_action_recorded"
					s.Qualification = "historical_migration_process_only_not_independent_schema_current_health_application_or_target"
					if a.succeeded {
						s.Status = "isolated_scope_migration_process_succeeded"
					}
					if a.failed {
						s.Status = "isolated_scope_migration_process_failed"
					}
					if a.removed {
						s.Status = "isolated_scope_database_task_removed"
					}
					if a.pending != "" {
						s.Status = "isolated_scope_database_task_action_uncertain"
					}
					if task.record.Task.Kind == engine.DatabaseSchemaCheck {
						s.Qualification = "historical_schema_check_process_and_removal_only_not_current_health_application_or_target"
						if s.SchemaQualified {
							s.Status = "isolated_scope_schema_qualified"
						} else if a.failed {
							s.Status = "isolated_scope_schema_check_failed"
						}
					}
					if role, _, ok := engine.StoreTaskContext(task.record.Task.Kind); ok {
						s.Qualification = "historical_store_namespace_and_audit_qualification_only_not_application_health_or_target"
						if a.failed {
							s.Status = "isolated_scope_store_task_failed"
						}
						if storeQualified(l.events, role) {
							s.Status = "isolated_scope_store_qualified"
						}
					}
					if bootstrapKind(task.record.Task.Kind) {
						s.Qualification = "historical_bootstrap_process_only_not_application_health_or_target"
						if a.failed {
							s.Status = "isolated_scope_bootstrap_task_failed"
						}
						if s.BootstrapQualified {
							s.Status = "isolated_scope_bootstrap_qualified"
						}
					}
					if task.record.Task.Kind == engine.DatabaseRuntimePrepare || task.record.Task.Kind == engine.DatabaseRuntimeCheck {
						s.Qualification = "historical_runtime_access_process_lineage_only_not_current_application_health_or_target"
						if a.failed {
							s.Status = "isolated_scope_runtime_access_task_failed"
						}
						if s.RuntimeAccessQualified {
							s.Status = "isolated_scope_runtime_access_qualified"
						}
					}
				}
			}

		}
	}
	if ctx.Err() != nil {
		return Status{}, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	if restoreState(l.events).record != nil {
		r := l.restoreReservationResult()
		s.Restore = &r
		if r.Pending {
			s.Status = "isolated_scope_" + r.Status
			s.Qualification = r.Qualification
		}
	}
	if restoreStoresState(l.events).record != nil {
		r := l.restoreStoresResult()
		s.RestoreStores = &r
	}
	if restoreDatabaseState(l.events).record != nil {
		r := l.restoreDatabaseResult()
		s.RestoreDatabase = &r
	}
	if restoreSQLState(l.events).record != nil {
		r := l.restoreSQLResult()
		s.RestoreSQL = &r
	}
	if controlReplacementState(l.events).record != nil {
		r := l.controlReplacementResult()
		s.ControlReplacement = &r
	}
	if tlsProjectionState(l.events).record != nil {
		r := l.tlsProjectionResult()
		s.TLSProjection = &r
		s.Status = "isolated_scope_" + r.Status
		s.Qualification = r.Qualification
	}
	if liveDatabaseState(l.events).record != nil {
		r := l.liveDatabaseResult()
		s.RestoredLiveDatabase = &r
		s.Status = "isolated_scope_" + r.Status
		s.Qualification = r.Qualification
	}
	if currentStoresState(l.events).record != nil {
		r := l.currentStoresResult()
		s.RestoredCurrentStores = &r
		s.Status = "isolated_scope_" + r.Status
		s.Qualification = r.Qualification
	}
	applyRecordedProcessStatus(&s, l.events)
	if cumulativeUpgradeState(l.events).record != nil {
		r := l.cumulativeUpgradeResult()
		s.CumulativeUpgrade = &r
		s.Status = r.Status
		s.Qualification = r.Qualification
	}
	return s, nil
}

func Create(ctx context.Context, c configuration.Config) (Status, error) {
	return create(ctx, c, func(ctx context.Context, c configuration.Config, destination string) error {
		_, e := bundle.Unpack(ctx, c.Verification, c.BundleFile, destination, c.ExpectedArchiveSHA256, c.ExpectedManifestSHA256)
		return e
	})
}

type releasePreparer func(context.Context, configuration.Config, string) error

func create(ctx context.Context, c configuration.Config, prepare releasePreparer) (Status, error) {
	if e := inputs(c); e != nil {
		return Status{}, e
	}
	if _, e := os.Lstat(c.ScopeRoot); !os.IsNotExist(e) {
		return Status{}, failure("OFFLINE_SCOPE_EXISTING_ROOT_PRESERVED")
	}
	verified, e := bundle.Inspect(ctx, c.Verification, c.BundleFile, c.ExpectedArchiveSHA256, c.ExpectedManifestSHA256)
	if e != nil {
		return Status{}, e
	}
	id, e := nonce()
	if e != nil {
		return Status{}, e
	}
	operation, e := nonce()
	if e != nil {
		return Status{}, e
	}
	if ctx.Err() != nil {
		return Status{}, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	if e = os.Mkdir(c.ScopeRoot, 0700); e != nil {
		return Status{}, failure("OFFLINE_SCOPE_EXISTING_ROOT_PRESERVED")
	}
	root, e := openPrivateDirectory(c.ScopeRoot)
	if e != nil {
		return Status{}, failure("OFFLINE_SCOPE_PARTIAL_ROOT_PRESERVED")
	}
	l := &locked{root: root, config: c, marker: Marker{FormatVersion: scopeFormatVersion, ScopeID: id, Purpose: purpose, ConfigurationSHA256: c.Fingerprint(), ArchiveSHA256: c.ExpectedArchiveSHA256, ManifestSHA256: c.ExpectedManifestSHA256, Profile: verified.Profile, ReleaseID: verified.ReleaseID, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), OwnerUID: os.Geteuid()}}
	defer l.close()
	if e = writeNew(root, lockName, []byte(id+"\n")); e != nil {
		return Status{}, e
	}
	l.lock, e = root.OpenFile(lockName, os.O_RDWR, 0)
	if e != nil || syscall.Flock(int(l.lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return Status{}, failure("OFFLINE_SCOPE_LOCK_UNAVAILABLE")
	}
	if e = writeNew(root, markerName, encode(l.marker)); e != nil {
		return Status{}, e
	}
	journal, e := root.OpenFile(journalName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return Status{}, failure("OFFLINE_SCOPE_JOURNAL_OCCUPIED")
	}
	journal.Close()
	if e = l.append("scope_create", "completed", operation, ""); e != nil {
		return Status{}, e
	}
	for _, name := range []string{c.ScopeRoot, filepath.Dir(c.ScopeRoot)} {
		folder, e := os.Open(name)
		if e != nil {
			return Status{}, failure("OFFLINE_SCOPE_DIRECTORY_UNAVAILABLE")
		}
		se := folder.Sync()
		folder.Close()
		if se != nil {
			return Status{}, failure("OFFLINE_SCOPE_DIRECTORY_DURABILITY_UNCERTAIN")
		}
	}
	operation, e = nonce()
	if e != nil {
		return Status{}, e
	}
	if e = l.append("release_prepare", "started", operation, ""); e != nil {
		return Status{}, e
	}
	destination := filepath.Join(c.ScopeRoot, "release")
	e = prepare(ctx, c, destination)
	if e == nil {
		_, e = bundle.VerifyPrepared(ctx, c.Verification, destination, c.ExpectedArchiveSHA256, c.ExpectedManifestSHA256)
	}
	if e == nil && ctx.Err() != nil {
		e = failure("RELEASE_PREPARATION_COMMIT_UNCERTAIN")
	}
	if e != nil {
		code := bundle.ErrorCode(e)
		outcome := "failed"
		if strings.Contains(code, "UNCERTAIN") {
			outcome = "uncertain"
		}
		if ce := l.append("release_prepare", outcome, operation, code); ce != nil {
			return Status{}, ce
		}
		return Status{}, e
	}
	if e = l.append("release_prepare", "completed", operation, ""); e != nil {
		return Status{}, e
	}
	if ctx.Err() != nil {
		return Status{}, failure("OFFLINE_SCOPE_COMMIT_UNCERTAIN")
	}
	return l.status(), nil
}
