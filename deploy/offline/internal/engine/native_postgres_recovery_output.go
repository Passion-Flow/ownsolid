package engine

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"reflect"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// These types retain the ORIGINAL native completion, separately from readonly
// observations. Their bytes never enter public TaskOutputObservation JSON.
type NativePostgreSQLBackupCompletion struct {
	Format         string `json:"format"`
	BackupID       string `json:"backup_id"`
	ManifestSHA256 string `json:"manifest_sha256"`
	ReadbackSHA256 string `json:"readback_sha256"`
	CompletedAt    string `json:"completed_at"`
	Qualification  string `json:"qualification"`
	OriginalSHA256 string `json:"-"`
	Original       []byte `json:"-"`
}

type NativePostgreSQLRestoreCompletion struct {
	Format              string                      `json:"format"`
	RestoreID           string                      `json:"restore_id"`
	IntentSHA256        string                      `json:"intent_sha256"`
	FilesSHA256         string                      `json:"files_sha256"`
	ManifestSHA256      string                      `json:"manifest_sha256"`
	ArchiveSHA256       string                      `json:"archive_sha256"`
	TargetBindingSHA256 string                      `json:"target_binding_sha256"`
	PostDataSHA256      string                      `json:"post_data_sha256"`
	RuntimeContext      wire.RestoredRuntimeContext `json:"runtime_context"`
	CompletedAt         string                      `json:"completed_at"`
	CurrentLockHeld     bool                        `json:"current_lock_held"`
	Qualification       string                      `json:"qualification"`
	OriginalSHA256      string                      `json:"-"`
	Original            []byte                      `json:"-"`
}

func decodeNativePostgreSQLRecoveryOutput(raw []byte, t DatabaseTask) (TaskOutputObservation, error) {
	bad := failure("OFFLINE_ENGINE_NATIVE_RECOVERY_OUTPUT_INVALID")
	out := TaskOutputObservation{Bytes: len(raw), Qualification: "original_native_output_transport_only_not_task_exit_or_scope_qualification"}
	if !t.validNativePostgreSQLRecovery() || len(raw) == 0 || int64(len(raw)) > t.nativeRecoveryPolicy().MaximumManifestBytes {
		return out, bad
	}
	p := t.NativePostgreSQLRecovery
	switch t.Kind {
	case NativePostgreSQLBackup:
		var v NativePostgreSQLBackupCompletion
		if wire.StrictJSON(raw, &v) != nil || v.Format != "projectone.postgres.backup-complete.v1" || v.BackupID != p.BackupRequest.BackupID || !validNativeObservationDigests(v.ManifestSHA256, v.ReadbackSHA256) || !nativeObservationTime(v.CompletedAt) || v.Qualification == "" {
			return out, bad
		}
		v.Original, v.OriginalSHA256 = append([]byte(nil), raw...), wire.Digest(raw)
		out.NativePostgreSQLBackupCompletion = &v
	case NativePostgreSQLRestore:
		var v NativePostgreSQLRestoreCompletion
		r := p.RestoreRequest
		if wire.StrictJSON(raw, &v) != nil || v.Format != "projectone.postgres.restore-complete.v1" || v.RestoreID != r.RestoreID || v.ManifestSHA256 != r.SourceManifestSHA256 || v.CurrentLockHeld || !validNativeObservationDigests(v.IntentSHA256, v.FilesSHA256, v.ArchiveSHA256, v.TargetBindingSHA256, v.PostDataSHA256) || !nativeObservationTime(v.CompletedAt) || v.Qualification == "" {
			return out, bad
		}
		// The original completion must explicitly report its released lock.
		// Missing or case-aliased JSON must not inherit Go's false zero value.
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || !bytes.Equal(bytes.TrimSpace(fields["current_lock_held"]), []byte("false")) {
			return out, bad
		}
		c := v.RuntimeContext
		if !c.Valid() || c.ApplicationProfile != wire.MVPPostgreSQLCPUApplicationProfile || c.RestoreID != r.RestoreID || c.TargetScopeID != r.TargetScopeID || c.BackupManifestSHA256 != r.SourceManifestSHA256 || c.SQLArchiveSHA256 != v.ArchiveSHA256 || c.TargetDatabase != r.TargetDatabase || c.TargetOperator != r.TargetOperator || c.Reason != r.Reason {
			return out, bad
		}
		v.Original, v.OriginalSHA256 = append([]byte(nil), raw...), wire.Digest(raw)
		out.NativePostgreSQLRestoreCompletion = &v
	case NativePostgreSQLRestoreVerify:
		r := p.RestoreRequest
		request, _ := json.Marshal(r)
		v, e := DecodeNativePostgreSQLRestoreObservation(raw, t.nativeRecoveryPolicy().MaximumManifestBytes, r.RestoreID, wire.Digest(append(request, '\n')), r.SourceManifestSHA256, p.BackupReference.SourceManifestSHA256)
		if e != nil || v.NativePostgreSQL != nil && !reflect.DeepEqual(v.NativePostgreSQL.Backup, *p.BackupReference) {
			return out, bad
		}
		out.NativePostgreSQLRestore = &v
	default:
		return out, bad
	}
	return out, nil
}

func readNativePostgreSQLRecoveryOutput(reader io.Reader, t DatabaseTask) (TaskOutputObservation, error) {
	bad := failure("OFFLINE_ENGINE_NATIVE_RECOVERY_OUTPUT_INVALID")
	if !t.validNativePostgreSQLRecovery() {
		return TaskOutputObservation{}, bad
	}
	maximum := t.nativeRecoveryPolicy().MaximumManifestBytes
	var stdout bytes.Buffer
	var total int64
	frames := 0
	for {
		var header [dockerOutputHeaderBytes]byte
		n, e := io.ReadFull(reader, header[:])
		if e == io.EOF && n == 0 {
			break
		}
		if e != nil || header[0] != 1 && header[0] != 2 || header[1] != 0 || header[2] != 0 || header[3] != 0 {
			return TaskOutputObservation{}, bad
		}
		frames++
		size := int64(binary.BigEndian.Uint32(header[4:]))
		if frames > maximumTaskOutputFrames || size > maximum-total {
			return TaskOutputObservation{}, bad
		}
		body := make([]byte, int(size))
		if _, e = io.ReadFull(reader, body); e != nil {
			return TaskOutputObservation{}, bad
		}
		total += size
		if header[0] == 2 {
			if len(bytes.TrimSpace(body)) != 0 {
				return TaskOutputObservation{Bytes: int(total), Redacted: true}, bad
			}
		} else {
			stdout.Write(body)
		}
	}
	out, e := decodeNativePostgreSQLRecoveryOutput(stdout.Bytes(), t)
	out.Bytes = int(total)
	if e != nil {
		return TaskOutputObservation{Bytes: int(total), Redacted: true}, bad
	}
	return out, nil
}

func (t DatabaseTask) freezeNativeRecovery() DatabaseTask {
	if t.NativePostgreSQLRecovery == nil {
		return t
	}
	p := *t.NativePostgreSQLRecovery
	if p.BackupRequest != nil {
		v := *p.BackupRequest
		p.BackupRequest = &v
	}
	if p.RestoreRequest != nil {
		v := *p.RestoreRequest
		v.TargetRoots = append([]wire.PostgreSQLRestoreRoot(nil), v.TargetRoots...)
		p.RestoreRequest = &v
	}
	if p.BackupReference != nil {
		v := *p.BackupReference
		if v.Registry != nil {
			q := *v.Registry
			v.Registry = &q
		}
		p.BackupReference = &v
	}
	if p.RecordsOwner != nil {
		v := *p.RecordsOwner
		p.RecordsOwner = &v
	}
	if p.Registry != nil {
		v := *p.Registry
		p.Registry = &v
	}
	if p.PreparedRelease != nil {
		v := *p.PreparedRelease
		p.PreparedRelease = &v
	}
	if p.Scaffold != nil {
		v := *p.Scaffold
		p.Scaffold = &v
	}
	t.NativePostgreSQLRecovery = &p
	if t.Peers != nil {
		peers := map[string]string{}
		for k, v := range t.Peers {
			peers[k] = v
		}
		t.Peers = peers
	}
	return t
}
