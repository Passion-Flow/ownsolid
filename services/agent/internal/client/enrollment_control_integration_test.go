//go:build repository_integration

package client

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/agent/internal/state"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

func TestR25ActualEnrollmentControl(t *testing.T) {
	path := os.Getenv("PROJECT_ONE_ENROLLMENT_CONTROL_PROBE")
	if path == "" {
		t.Skip("Explicit private actual control probe input required")
	}
	resolved, e := filepath.EvalSymlinks(path)
	if e != nil || !filepath.IsAbs(path) || filepath.Clean(path) != path || resolved != path {
		t.Fatal("PROBE_PRIVATE_CANONICAL_INPUT_REQUIRED")
	}
	parent, e := os.Stat(filepath.Dir(path))
	if e != nil || parent.Mode().Perm() != 0700 {
		t.Fatal("PROBE_PRIVATE_PARENT_REQUIRED")
	}
	parentMeta, owned := parent.Sys().(*syscall.Stat_t)
	if !owned || parentMeta.Uid != uint32(os.Geteuid()) {
		t.Fatal("PROBE_PRIVATE_PARENT_OWNER_REQUIRED")
	}
	inputInfo, e := os.Lstat(path)
	if e != nil || inputInfo.Mode().Perm() != 0600 {
		t.Fatal("PROBE_PRIVATE_MODE_REQUIRED")
	}
	meta, ok := inputInfo.Sys().(*syscall.Stat_t)
	if !ok || meta.Nlink != 1 || meta.Uid != uint32(os.Geteuid()) {
		t.Fatal("PROBE_PRIVATE_OWNED_INPUT_REQUIRED")
	}
	raw, e := state.ProtectedFile(path)
	if e != nil {
		t.Fatal("PROBE_PRIVATE_INPUT_READ_FAILED")
	}
	var input struct {
		Kind     string          `json:"kind"`
		Origin   string          `json:"origin"`
		CAFile   string          `json:"ca_file"`
		Input    wire.Enrollment `json:"input"`
		Expected string          `json:"expected"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF || input.Kind != "projectone-node-enrollment-control-probe-v1" ||
		(input.Expected != "acknowledged" && input.Expected != "uncertain") {
		t.Fatal("PROBE_STRICT_INPUT_REQUIRED")
	}
	origin, e := url.Parse(input.Origin)
	if e != nil || origin.Scheme != "https" || (origin.Hostname() != "127.0.0.1" && origin.Hostname() != "localhost") || origin.Port() == "" {
		t.Fatal("PROBE_ISOLATED_HTTPS_ORIGIN_REQUIRED")
	}
	if _, e = state.ProtectedFile(input.CAFile); e != nil {
		t.Fatal("PROBE_PRIVATE_CA_REQUIRED")
	}
	connection, e := New(input.Origin, input.CAFile)
	if e != nil {
		t.Fatal("PROBE_ACTUAL_TRUSTED_CLIENT_START_FAILED")
	}
	defer connection.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	var enrolled wire.Enrolled
	e = connection.Post(ctx, "/agent/v1/enroll", input.Input, &enrolled)
	result := struct {
		Kind      string         `json:"kind"`
		Outcome   string         `json:"outcome"`
		RequestID string         `json:"request_id,omitempty"`
		Enrolled  *wire.Enrolled `json:"enrolled,omitempty"`
	}{Kind: "projectone-node-enrollment-control-result-v1", Outcome: input.Expected}
	if input.Expected == "uncertain" {
		var uncertain *EnrollmentUncertain
		if !errors.As(e, &uncertain) || !reflect.DeepEqual(enrolled, wire.Enrolled{}) {
			t.Fatal("PROBE_ACTUAL_UNCERTAINTY_OR_ZERO_OUTPUT_MISMATCH")
		}
		result.RequestID = uncertain.RequestID()
	} else {
		credential, decoded := base64.RawURLEncoding.DecodeString(enrolled.Credential)
		if e != nil || !enrollmentRequestID.MatchString(enrolled.NodeID) || enrolled.NodeID == "00000000-0000-0000-0000-000000000000" || decoded != nil || len(credential) != 32 || len(enrolled.Credential) != 43 ||
			enrolled.ProtocolVersion != input.Input.ProtocolVersion || enrolled.HeartbeatSeconds != wire.HeartbeatSeconds {
			t.Fatal("PROBE_ACTUAL_ACKNOWLEDGED_ENROLLMENT_INVALID")
		}
		result.Enrolled = &enrolled
	}
	output := filepath.Join(filepath.Dir(path), "agent-result.json")
	file, e := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		t.Fatal("PROBE_EXCLUSIVE_PRIVATE_OUTPUT_REQUIRED")
	}
	encode := json.NewEncoder(file).Encode(result)
	synced := file.Sync()
	closed := file.Close()
	if encode != nil || synced != nil || closed != nil {
		t.Fatal("PROBE_PRIVATE_OUTPUT_WRITE_FAILED")
	}
	directory, e := os.Open(filepath.Dir(path))
	if e != nil {
		t.Fatal("PROBE_OUTPUT_DIRECTORY_REQUIRED")
	}
	synced = directory.Sync()
	closed = directory.Close()
	if synced != nil || closed != nil {
		t.Fatal("PROBE_OUTPUT_DIRECTORY_SYNC_FAILED")
	}
	t.Log("Actual agent Client.Post completed against the declared isolated control endpoint; private result retained for independent owner readback")
}
