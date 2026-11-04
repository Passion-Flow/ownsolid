package training_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"testing"

	"github.com/google/uuid"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

func TestActualTrainingDatasetInputRangesReauthorizeAndBindImmutableInventory(t *testing.T) {
	f := setup(t)
	j := dispatched(t, f)
	l := claimed(t, f)
	ctx := context.Background()
	asset := l.Training.Inputs.Train
	input := wire.InputFileRequest{LeaseToken: l.LeaseToken, Kind: "dataset", Asset: asset.Path, Offset: 0, Length: asset.SizeBytes}
	download, e := f.s.InputFile(ctx, f.input.NodeID, f.credential, l.TaskID, input)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := io.ReadAll(io.NewSectionReader(download.File, download.Offset, download.Size))
	download.File.Close()
	if e != nil {
		t.Fatal(e)
	}
	digest := sha256.Sum256(raw)
	if download.Size != asset.SizeBytes || hex.EncodeToString(digest[:]) != asset.SHA256 || download.SHA256 != asset.SHA256 {
		t.Fatal("actual immutable source range/hash")
	}
	bad := input
	bad.Length--
	_, e = f.s.InputFile(ctx, f.input.NodeID, f.credential, l.TaskID, bad)
	expected(t, e, "TRAINING_TRANSFER_INVALID")
	bad = input
	bad.Asset = "../train.jsonl"
	_, e = f.s.InputFile(ctx, f.input.NodeID, f.credential, l.TaskID, bad)
	expected(t, e, "TRAINING_TRANSFER_INVALID")
	_, e = f.s.InputFile(ctx, f.input.NodeID, f.credential, uuid.NewString(), input)
	expected(t, e, "AGENT_LEASE_STALE")
	if _, e = f.s.Cancel(ctx, f.portal, f.input.WorkspaceID, j.ID, training.ActionRequest{ExpectedRevision: j.Revision, Reason: "Reauthorize every actual input range"}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	_, e = f.s.InputFile(ctx, f.input.NodeID, f.credential, l.TaskID, input)
	expected(t, e, "ACCESS_DENIED")
}

func TestActualTLSLeasedTrainingInputDownloadAndStrictBodyRefusal(t *testing.T) {
	f := setup(t)
	dispatched(t, f)
	l := claimed(t, f)
	handler := transport.New(f.s.Identity)
	handler.Datasets.Service.Store = f.s.Datasets.Store
	handler.Datasets.Service.Processor = f.s.Datasets.Processor
	server := httpfixture.NewTLSServer(t, handler)
	defer server.Close()
	asset := l.Training.Inputs.Validation
	input := wire.InputFileRequest{LeaseToken: l.LeaseToken, Kind: "dataset", Asset: asset.Path, Length: asset.SizeBytes}
	body, _ := json.Marshal(input)
	call := func(body []byte, status int) {
		t.Helper()
		request, e := http.NewRequest("POST", httpfixture.AdminOrigin+"/agent/v1/sites/default/cells/default/nodes/"+f.input.NodeID+"/tasks/"+l.TaskID+"/training-input-files", bytes.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		request.Host = "admin.example.invalid"
		request.Header.Set("Origin", "https://admin.example.invalid")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+f.credential)
		response, e := server.Client().Do(request)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		raw, e := io.ReadAll(response.Body)
		if e != nil || response.StatusCode != status {
			t.Fatalf("actual transfer status=%d expected=%d error=%v", response.StatusCode, status, e)
		}
		if status == 200 {
			digest := sha256.Sum256(raw)
			if response.Header.Get("X-Artifact-SHA256") != hex.EncodeToString(digest[:]) || response.Header.Get("X-Artifact-SHA256") != asset.SHA256 || response.Header.Get("Content-Length") != strconv.Itoa(len(raw)) || response.Header.Get("Content-Type") != "application/octet-stream" {
				t.Fatal("actual range headers/digest")
			}
		}
	}
	call(body, 200)
	var fields map[string]any
	json.Unmarshal(body, &fields)
	fields["Asset"] = fields["asset"]
	delete(fields, "asset")
	bad, _ := json.Marshal(fields)
	call(bad, 400)
	call(append([]byte(`{"unknown":true,`), body[1:]...), 400)
	if _, e := f.s.DB.Exec(context.Background(), "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", f.portal.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	call(body, 403)
}
