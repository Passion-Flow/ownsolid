package engine

import (
	"bytes"
	"encoding/binary"
	"io"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Original native observation bytes are private custody, never a public Scope
// status or live physical/SQL authority. Original locks are already
// released; final native restore must reacquire and compare all four identities.
type NativePostgreSQLRestoreRootsInspectionOutput struct {
	Observation    wire.PostgreSQLRestoreRootsInspectionObservation `json:"-"`
	OriginalSHA256 string                                           `json:"-"`
	Original       []byte                                           `json:"-"`
}

func decodeNativePostgreSQLRestoreRootsInspectionOutput(raw []byte, t DatabaseTask) (TaskOutputObservation, error) {
	bad := failure("OFFLINE_ENGINE_NATIVE_ROOTS_OUTPUT_INVALID")
	if !t.validNativePostgreSQLRestoreRootsInspection() || len(raw) == 0 || int64(len(raw)) > t.NativePostgreSQLRestoreRootsInspection.Request.Policy.MaximumRecordBytes {
		return TaskOutputObservation{}, bad
	}
	p := t.NativePostgreSQLRestoreRootsInspection
	var observed wire.PostgreSQLRestoreRootsInspectionObservation
	if wire.StrictJSON(raw, &observed) != nil || !observed.ValidFor(p.Request) {
		return TaskOutputObservation{}, bad
	}
	for i, root := range observed.Roots {
		if root.Path != p.Stores[i].Target {
			return TaskOutputObservation{}, bad
		}
	}
	result := NativePostgreSQLRestoreRootsInspectionOutput{Observation: observed, OriginalSHA256: wire.Digest(raw), Original: append([]byte(nil), raw...)}
	return TaskOutputObservation{NativePostgreSQLRestoreRootsInspection: &result, Bytes: len(raw), Qualification: "original_readonly_roots_transport_only_released_locks_not_continuous_custody_or_Scope_authority"}, nil
}

func readNativePostgreSQLRestoreRootsInspectionOutput(reader io.Reader, t DatabaseTask) (TaskOutputObservation, error) {
	bad := failure("OFFLINE_ENGINE_NATIVE_ROOTS_OUTPUT_INVALID")
	if !t.validNativePostgreSQLRestoreRootsInspection() {
		return TaskOutputObservation{}, bad
	}
	maximum := t.NativePostgreSQLRestoreRootsInspection.Request.Policy.MaximumRecordBytes
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
	result, e := decodeNativePostgreSQLRestoreRootsInspectionOutput(stdout.Bytes(), t)
	if e != nil {
		return TaskOutputObservation{Bytes: int(total), Redacted: true}, bad
	}
	result.Bytes = int(total)
	return result, nil
}
