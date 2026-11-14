package modelpreparations

import (
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"

	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

type transferReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r transferReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.reader.Read(p)
}

// InputFile opens only current frozen module-owned sources. Expensive source verification occurs outside authority locks.
func (s *Service) InputFile(ctx context.Context, node, credential, task string, input wire.InputFileRequest) (httpserver.Download, error) {
	var out httpserver.Download
	if input.Validate() != nil {
		return out, fail("MODEL_PREPARATION_TRANSFER_INVALID")
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().TransferSeconds))
	defer cancel()
	if input.Kind == "model" {
		e := s.withMachineLease(ctx, node, credential, task, input.LeaseToken, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, p Preparation, c wire.Command) error {
			var e error
			out, e = s.Models.OpenTransferWithin(ctx, tx, w.TenantID, w.ID, p.ModelID, agentwire.ModelFileRequest{LeaseToken: input.LeaseToken, File: input.Asset, Offset: input.Offset, Length: input.Length})
			return e
		})
		return preparationDownloadOutcome(out, e)
	}
	var snapshot preparationInputs
	var version datasets.Version
	var commandSHA string
	e := s.withMachineLease(ctx, node, credential, task, input.LeaseToken, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, p Preparation, c wire.Command) error {
		var e error
		snapshot, version, e = s.preparationSourcesWithin(ctx, tx, w, CreateRequest{NodeID: node, TrainingJobID: p.TrainingJobID, HeldoutID: p.HeldoutID})
		if e != nil {
			return e
		}
		if snapshot.digest() != p.InputSHA256 {
			return fail("MODEL_PREPARATION_INPUT_CHANGED")
		}
		commandSHA, e = c.Digest()
		return e
	})
	if e != nil {
		return out, e
	}
	release, e := s.acquireScan(ctx)
	if e != nil {
		return out, e
	}
	defer release()
	var file *os.File
	var asset datawire.Asset
	if input.Kind == "adapter" {
		file, asset, e = s.Training.OpenAcceptedAdapterAsset(ctx, trainingSource(snapshot), input.Asset)
	} else {
		asset = snapshot.inventory().Heldout
		if input.Asset != asset.Path {
			return out, fail("MODEL_PREPARATION_TRANSFER_INVALID")
		}
		var size int64
		file, size, e = s.Datasets.OpenPublishedSource(ctx, version, min(s.policy().ScanMaximumBytes, datawire.MaximumBytesCeiling))
		if e == nil && size != asset.SizeBytes {
			e = fail("MODEL_PREPARATION_INPUT_CHANGED")
		}
	}
	keep := false
	defer func() {
		if !keep && file != nil {
			file.Close()
		}
	}()
	if e != nil {
		return out, e
	}
	if input.ValidateAsset(asset) != nil {
		return out, fail("MODEL_PREPARATION_TRANSFER_INVALID")
	}
	before, e := file.Stat()
	if e != nil {
		return out, fail("MODEL_PREPARATION_INPUT_CHANGED")
	}
	digest := sha256.New()
	size, e := io.Copy(digest, transferReader{ctx, io.NewSectionReader(file, input.Offset, input.Length)})
	if e != nil || size != input.Length {
		return out, fail("MODEL_PREPARATION_TRANSFER_FAILED")
	}
	after, e := file.Stat()
	if e != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return out, fail("MODEL_PREPARATION_INPUT_CHANGED")
	}
	e = s.withMachineLease(ctx, node, credential, task, input.LeaseToken, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, p Preparation, c wire.Command) error {
		sha, e := c.Digest()
		if e != nil {
			return e
		}
		if sha != commandSHA || p.InputSHA256 != snapshot.digest() {
			return fail("MODEL_PREPARATION_ATTEMPT_STALE")
		}
		current, _, e := s.preparationSourcesWithin(ctx, tx, w, CreateRequest{NodeID: node, TrainingJobID: p.TrainingJobID, HeldoutID: p.HeldoutID})
		if e != nil {
			return e
		}
		if current.digest() != snapshot.digest() {
			return fail("MODEL_PREPARATION_INPUT_CHANGED")
		}
		return nil
	})
	if e != nil {
		return out, e
	}
	keep = true
	return httpserver.Download{File: file, Offset: input.Offset, Size: input.Length, SHA256: hex.EncodeToString(digest.Sum(nil)), ContentType: "application/octet-stream"}, nil
}

func preparationDownloadOutcome(out httpserver.Download, e error) (httpserver.Download, error) {
	if e != nil {
		if out.File != nil {
			out.File.Close()
		}
		return httpserver.Download{}, e
	}
	return out, nil
}
