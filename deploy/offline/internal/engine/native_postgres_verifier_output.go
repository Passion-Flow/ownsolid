package engine

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// The native request digest follows the original owner's canonical JSON+LF.
// This binds transport to its frozen input; it does not validate a backup.
func nativeVerifierRequestSHA256(r wire.PostgreSQLBackupRequest) string {
	raw, e := json.Marshal(r)
	if e != nil {
		return ""
	}
	return wire.Digest(append(raw, '\n'))
}

func readNativePostgreSQLVerifierOutput(reader io.Reader, t DatabaseTask) (TaskOutputObservation, error) {
	bad := failure("OFFLINE_ENGINE_NATIVE_VERIFIER_OUTPUT_INVALID")
	if !t.validNativePostgreSQLVerifier() {
		return TaskOutputObservation{}, bad
	}
	p := t.NativePostgreSQLBackupVerifier
	maximum := p.Request.Policy.MaximumManifestBytes
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
		length := int64(binary.BigEndian.Uint32(header[4:]))
		if frames > maximumTaskOutputFrames || length > maximum-total {
			return TaskOutputObservation{}, bad
		}
		body := make([]byte, int(length))
		if _, e = io.ReadFull(reader, body); e != nil {
			return TaskOutputObservation{}, bad
		}
		total += length
		if header[0] == 2 {
			if len(bytes.TrimSpace(body)) != 0 {
				return TaskOutputObservation{Bytes: int(total), Redacted: true}, bad
			}
		} else {
			stdout.Write(body)
		}
	}
	observed, e := DecodeNativePostgreSQLBackupObservation(stdout.Bytes(), maximum, p.Request.BackupID, nativeVerifierRequestSHA256(p.Request), p.CandidateSHA256, p.Request.SourceManifestSHA256)
	if e != nil {
		return TaskOutputObservation{Bytes: int(total), Redacted: true}, bad
	}
	return TaskOutputObservation{Bytes: int(total), NativePostgreSQLBackup: &observed, Qualification: "original_native_observation_transport_only_not_task_exit_or_scope_qualification"}, nil
}

func (t DatabaseTask) freezeNativeVerifier() DatabaseTask {
	if t.NativePostgreSQLBackupVerifier != nil {
		p := *t.NativePostgreSQLBackupVerifier
		if p.Placeholder != nil {
			q := *p.Placeholder
			p.Placeholder = &q
		}
		t.NativePostgreSQLBackupVerifier = &p
		if t.Peers != nil {
			peers := make(map[string]string, len(t.Peers))
			for k, v := range t.Peers {
				peers[k] = v
			}
			t.Peers = peers
		}
	}
	return t
}
