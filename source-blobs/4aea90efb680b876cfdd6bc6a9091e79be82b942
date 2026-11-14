package training

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"

	"database/sql"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

type acceptedCheckpoint struct {
	ID         string
	Descriptor UploadDescriptor
	Receipt    wire.CheckpointReceipt
	Raw        []byte
}

// acceptedCheckpoint resolves only the immutable accepted same-job reference, never caller-provided checkpoint bytes.
func (s *Service) acceptedCheckpointWithin(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, j Job, command wire.Command, id string) (acceptedCheckpoint, error) {
	var out acceptedCheckpoint
	if !datawire.ValidID(id) {
		return out, fail("TRAINING_CHECKPOINT_UNAVAILABLE")
	}
	var attempt, sha, storage string
	var step int64
	var inventory []byte
	e := tx.QueryRow(ctx, transferStatement(tx, `SELECT r.id::text,r.attempt_id::text,r.global_step,r.receipt_sha256,r.file_inventory,r.storage_key,u.manifest_bytes FROM training_checkpoints r JOIN training_uploads u ON u.id=r.id AND u.attempt_id=r.attempt_id WHERE r.tenant_id=$1 AND r.workspace_id=$2 AND r.job_id=$3 AND r.id=$4 AND u.state='accepted' AND u.kind='checkpoint'`, `SELECT r.id,r.attempt_id,r.global_step,r.receipt_sha256,r.file_inventory,r.storage_key,u.manifest_bytes FROM training_checkpoints r JOIN training_uploads u ON u.id=r.id AND u.attempt_id=r.attempt_id WHERE r.tenant_id=? AND r.workspace_id=? AND r.job_id=? AND r.id=? AND u.state='accepted' AND u.kind='checkpoint'`, []any{w.TenantID, w.ID, j.ID, id}, []any{w.TenantID, w.ID, j.ID, id})).Scan(&out.ID, &attempt, &step, &sha, &inventory, &storage, &out.Raw)
	if errors.Is(e, sql.ErrNoRows) {
		return out, fail("TRAINING_CHECKPOINT_UNAVAILABLE")
	}
	if e != nil {
		return out, e
	}
	request, e := command.ContainerRequest()
	if e != nil {
		return out, e
	}
	out.Receipt, e = wire.DecodeCheckpoint(out.Raw, request, agentwire.ApprovedModel().Repository)
	if e != nil || rawDigest(out.Raw) != sha || out.Receipt.GlobalStep != step || wire.DecodeStrict(inventory, &out.Descriptor) != nil {
		return acceptedCheckpoint{}, fail("TRAINING_CHECKPOINT_INVALID")
	}
	d := out.Descriptor
	if d.Validate(j.Recipe.Resources.MaximumOutputBytes, j.Recipe.Resources.MaximumOutputFiles) != nil || d.UploadID != id || d.Kind != "checkpoint" || d.Scope != command.Scope || d.AttemptID != attempt || d.GlobalStep != step || d.ManifestSHA256 != sha || storage != publishedDirectory(d) {
		return acceptedCheckpoint{}, fail("TRAINING_CHECKPOINT_INVALID")
	}
	expected := append(append([]datawire.Asset{}, out.Receipt.Files...), datawire.Asset{Path: "projectone-checkpoint.json", SizeBytes: int64(len(out.Raw)), SHA256: sha})
	actualJSON, _ := json.Marshal(d.Files)
	expectedJSON, _ := json.Marshal(expected)
	if string(actualJSON) != string(expectedJSON) {
		return acceptedCheckpoint{}, fail("TRAINING_CHECKPOINT_INVALID")
	}
	return out, nil
}

func (c acceptedCheckpoint) resume() wire.ResumeInput {
	return wire.ResumeInput{CheckpointID: c.ID, GlobalStep: c.Receipt.GlobalStep, ReceiptSHA256: c.Descriptor.ManifestSHA256, Files: append([]datawire.Asset{}, c.Descriptor.Files...)}
}

// checkpointInputFile verifies physical bytes outside business locks and repeats current authority before returning an open range.
func (s *Service) checkpointInputFile(ctx context.Context, node, credential, task string, input wire.InputFileRequest) (httpserver.Download, error) {
	var out httpserver.Download
	var checkpoint acceptedCheckpoint
	if s.Store == nil {
		return out, fail("TRAINING_STORAGE_UNAVAILABLE")
	}
	admit := func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, j Job, command wire.Command) error {
		if command.Resume == nil {
			return fail("TRAINING_CHECKPOINT_UNAVAILABLE")
		}
		current, e := s.acceptedCheckpointWithin(ctx, tx, w, j, command, command.Resume.CheckpointID)
		if e != nil {
			return e
		}
		expected, _ := json.Marshal(current.resume())
		given, _ := json.Marshal(command.Resume)
		if string(expected) != string(given) {
			return fail("TRAINING_CHECKPOINT_INVALID")
		}
		if checkpoint.ID != "" && checkpoint.Descriptor.ManifestSHA256 != current.Descriptor.ManifestSHA256 {
			return fail("TRAINING_ATTEMPT_STALE")
		}
		checkpoint = current
		return nil
	}
	if e := s.withMachineLease(ctx, node, credential, task, input.LeaseToken, admit); e != nil {
		return out, e
	}
	file, asset, e := s.Store.OpenPublishedAsset(ctx, checkpoint.Descriptor, input.Asset)
	if e != nil {
		return out, e
	}
	keep := false
	defer func() {
		if !keep {
			file.Close()
		}
	}()
	if input.ValidateAsset(asset) != nil {
		return out, fail("TRAINING_TRANSFER_INVALID")
	}
	hash := sha256.New()
	size, e := io.Copy(hash, contextReader{ctx: ctx, reader: io.NewSectionReader(file, input.Offset, input.Length)})
	if e != nil || size != input.Length {
		return out, fail("TRAINING_TRANSFER_FAILED")
	}
	if e = s.withMachineLease(ctx, node, credential, task, input.LeaseToken, admit); e != nil {
		return out, e
	}
	keep = true
	return httpserver.Download{File: file, Offset: input.Offset, Size: input.Length, SHA256: hex.EncodeToString(hash.Sum(nil)), ContentType: "application/octet-stream"}, nil
}
