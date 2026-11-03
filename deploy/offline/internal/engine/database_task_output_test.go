package engine

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func taskOutputFrame(stream byte, body string) []byte {
	raw := make([]byte, dockerOutputHeaderBytes+len(body))
	raw[0] = stream
	binary.BigEndian.PutUint32(raw[4:], uint32(len(body)))
	copy(raw[8:], body)
	return raw
}

func TestDatabaseTaskOutputFixedCodesRedactionAndFraming(t *testing.T) {
	raw := append(taskOutputFrame(1, "RUNTIME_ACCESS_"), taskOutputFrame(1, "VERIFIED\n")...)
	raw = append(raw, taskOutputFrame(2, "RUNTIME_ACCESS_INITIAL_INSTALLATION_REQUIRED\n")...)
	r, e := readDatabaseTaskOutput(bytes.NewReader(raw))
	if e != nil || r.StdoutCode != "RUNTIME_ACCESS_VERIFIED" || r.StderrCode != "RUNTIME_ACCESS_INITIAL_INSTALLATION_REQUIRED" || r.Redacted {
		t.Fatal(r, e)
	}
	for _, secret := range []string{"synthetic-private-credential", "AKIAEXPLICITSYNTHETICUPPERCASE", "RUNTIME_ACCESS_UNKNOWN_SECRET_SUFFIX", "RUNTIME_ACCESS_VERIFIED\nprivate-value"} {
		r, e = readDatabaseTaskOutput(bytes.NewReader(taskOutputFrame(2, secret)))
		if e != nil || !r.Redacted || r.StderrCode != "" {
			t.Fatal("unknown or credential-shaped output leaked", e)
		}
	}
	for _, raw := range [][]byte{{1, 2}, taskOutputFrame(3, "foreign"), taskOutputFrame(1, string(make([]byte, maximumTaskOutputBytes+1))), bytes.Repeat(taskOutputFrame(1, ""), maximumTaskOutputFrames+1)} {
		if _, e := readDatabaseTaskOutput(bytes.NewReader(raw)); e == nil {
			t.Fatal("malformed/unbounded output accepted")
		}
	}
	truncated := taskOutputFrame(2, "known")
	if _, e := readDatabaseTaskOutput(bytes.NewReader(truncated[:len(truncated)-1])); e == nil {
		t.Fatal("truncated frame accepted")
	}
}
