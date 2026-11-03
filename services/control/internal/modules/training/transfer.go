package training

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"reflect"

	"database/sql"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

// withMachineLease checks current human, Workspace, node credential, exact live attempt and shared GPU fence.
func (s *Service) withMachineLease(ctx context.Context, node, credential, task, token string, fn func(context.Context, transaction.Executor, workspaces.Workspace, Job, wire.Command) error) error {
	c, _, e := s.machineCandidate(ctx, node, task)
	if errors.Is(e, sql.ErrNoRows) {
		return fail("AGENT_LEASE_STALE")
	}
	if e != nil {
		return e
	}
	return s.delegatedRepositoryTransaction(ctx, c, func(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, n nodes.Node, j Job, valid bool) error {
		current, e := s.Nodes.CredentialWithin(ctx, tx, c.tenant, c.workspace, node, credential)
		if e != nil {
			return e
		}
		if !valid || current.Status != "active" || j.State != "preparing" && j.State != "running" || j.CancelRequestedAt != nil {
			return fail("ACCESS_DENIED")
		}
		_, command, e := s.Nodes.LeasedTrainingWithin(ctx, tx, current, task, token)
		if e != nil {
			return e
		}
		if e = bindAttempt(ctx, tx, j, command, task); e != nil {
			return e
		}
		return fn(ctx, tx, w, j, command)
	})
}

// InputFile returns only a frozen owning-module file range, never a supplied storage path/URL.
func (s *Service) InputFile(ctx context.Context, node, credential, task string, input wire.InputFileRequest) (httpserver.Download, error) {
	var out httpserver.Download
	if input.Validate() != nil {
		return out, fail("TRAINING_TRANSFER_INVALID")
	}
	if input.Kind == "checkpoint" {
		return s.checkpointInputFile(ctx, node, credential, task, input)
	}
	if input.Kind == "dataset" {
		return s.datasetInputFile(ctx, node, credential, task, input)
	}
	e := s.withMachineLease(ctx, node, credential, task, input.LeaseToken, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, j Job, command wire.Command) error {
		switch input.Kind {
		case "model":
			var e error
			out, e = s.Models.OpenTransferWithin(ctx, tx, w.TenantID, w.ID, j.ModelID, agentwire.ModelFileRequest{LeaseToken: input.LeaseToken, File: input.Asset, Offset: input.Offset, Length: input.Length})
			return e

		default:
			return fail("TRAINING_CHECKPOINT_UNAVAILABLE")
		}
	})
	if e != nil {
		if out.File != nil {
			out.File.Close()
		}
		return httpserver.Download{}, e
	}
	return out, nil
}

// verifyMachineInputFiles performs full immutable partition verification outside
// business locks; the following mutation independently repeats all live fences.
func (s *Service) verifyMachineInputFiles(ctx context.Context, node, credential, task, token string) error {
	var inputs datasets.TrainingInputs
	e := s.withMachineLease(ctx, node, credential, task, token, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace, j Job, command wire.Command) error {
		var e error
		inputs, e = s.Datasets.TrainingInputSnapshotWithin(ctx, q, w.TenantID, w.ID, j.DatasetID, j.DatasetVersionID, true)
		if e != nil {
			return e
		}
		if inputs.Version.ManifestSHA256 != command.DatasetManifestSHA256 || inputs.Train != command.Inputs.Train || inputs.Validation != command.Inputs.Validation {
			return fail("TRAINING_INPUT_UNAVAILABLE")
		}
		return nil
	})
	if e != nil {
		return e
	}
	return trainingRepositoryError(s.Datasets.VerifyTrainingInputSnapshot(ctx, inputs))
}

// datasetInputFile pins and hashes immutable bytes outside business locks, then
// rechecks current authority and the complete retained snapshot before publication.
func (s *Service) datasetInputFile(ctx context.Context, node, credential, task string, input wire.InputFileRequest) (httpserver.Download, error) {
	var snapshot datasets.TrainingInputs
	admit := func(ctx context.Context, q transaction.Executor, w workspaces.Workspace, j Job, command wire.Command) error {
		current, e := s.Datasets.TrainingInputSnapshotWithin(ctx, q, w.TenantID, w.ID, j.DatasetID, j.DatasetVersionID, true)
		if e != nil {
			return e
		}
		if current.Version.ManifestSHA256 != command.DatasetManifestSHA256 || current.Train != command.Inputs.Train || current.Validation != command.Inputs.Validation {
			return fail("TRAINING_INPUT_UNAVAILABLE")
		}
		if snapshot.Version.ID != "" && !reflect.DeepEqual(snapshot, current) {
			return fail("TRAINING_INPUT_UNAVAILABLE")
		}
		snapshot = current
		return nil
	}
	if e := s.withMachineLease(ctx, node, credential, task, input.LeaseToken, admit); e != nil {
		return httpserver.Download{}, e
	}
	file, asset, e := s.Datasets.OpenTrainingAssetContext(ctx, snapshot, input.Asset)
	if e != nil {
		return httpserver.Download{}, trainingRepositoryError(e)
	}
	keep := false
	defer func() {
		if !keep {
			file.Close()
		}
	}()
	if input.ValidateAsset(asset) != nil {
		return httpserver.Download{}, fail("TRAINING_TRANSFER_INVALID")
	}
	hash := sha256.New()
	n, e := io.Copy(hash, contextReader{ctx: ctx, reader: io.NewSectionReader(file, input.Offset, input.Length)})
	if e != nil || n != input.Length {
		return httpserver.Download{}, fail("TRAINING_TRANSFER_FAILED")
	}
	if e = s.withMachineLease(ctx, node, credential, task, input.LeaseToken, admit); e != nil {
		return httpserver.Download{}, e
	}
	keep = true
	return httpserver.Download{File: file, Offset: input.Offset, Size: input.Length, SHA256: hex.EncodeToString(hash.Sum(nil)), ContentType: "application/octet-stream"}, nil
}
