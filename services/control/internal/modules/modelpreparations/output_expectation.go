package modelpreparations

import (
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"

	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

type outputExpectation struct {
	Preparation Preparation
	Command     wire.Command
	Report      wire.PreparedResult
	Expected    wire.ResultExpectation
	BaseWeights *os.File
}

// prepareOutputExpectation resolves actual retained inputs and approved tokenizer bytes outside authority locks.
// Final acceptance must freshly recheck this complete command/source/event binding after all large-file IO.
func (s *Service) prepareOutputExpectation(ctx context.Context, node, credential, task, token string, metadata []byte, weights bool) (outputExpectation, error) {
	var out outputExpectation
	var version datasets.Version
	var tokenizerFile, configurationFile *os.File
	keep := false
	defer func() {
		if tokenizerFile != nil {
			tokenizerFile.Close()
		}
		if configurationFile != nil {
			configurationFile.Close()
		}
		if !keep && out.BaseWeights != nil {
			out.BaseWeights.Close()
		}
	}()
	e := s.withMachineLease(ctx, node, credential, task, token, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, p Preparation, c wire.Command) error {
		if len(metadata) < 2 || int64(len(metadata)) > c.Recipe.Resources.MaximumResultBytes {
			return fail("MODEL_PREPARATION_REPORT_INVALID")
		}
		sources, v, e := s.preparationSourcesWithin(ctx, tx, w, CreateRequest{NodeID: node, TrainingJobID: p.TrainingJobID, HeldoutID: p.HeldoutID})
		if e != nil {
			return e
		}
		if sources.digest() != p.InputSHA256 {
			return fail("MODEL_PREPARATION_INPUT_CHANGED")
		}
		out.Preparation, out.Command, version = p, c, v
		tokenizerFile, _, e = s.Models.OpenVerificationAssetWithin(ctx, tx, w.TenantID, w.ID, p.ModelID, "tokenizer_config.json", token)
		if e != nil {
			return e
		}
		configurationFile, _, e = s.Models.OpenVerificationAssetWithin(ctx, tx, w.TenantID, w.ID, p.ModelID, "config.json", token)
		if e != nil {
			return e
		}
		if weights {
			out.BaseWeights, _, e = s.Models.OpenVerificationAssetWithin(ctx, tx, w.TenantID, w.ID, p.ModelID, "model.safetensors", token)
		}
		return e
	})
	if e != nil {
		return outputExpectation{}, e
	}
	verified, e := s.verifyRetainedOutput(ctx, out, version, metadata, tokenizerFile, configurationFile)
	if e != nil {
		return outputExpectation{}, e
	}
	keep = true
	return verified, nil
}

// verifyRetainedOutput shares actual immutable input/report semantics across machine
// acceptance and an independently authorized human review snapshot.
func (s *Service) verifyRetainedOutput(ctx context.Context, out outputExpectation, version datasets.Version, metadata []byte, tokenizerFile, configurationFile *os.File) (outputExpectation, error) {
	var e error
	if e = s.verifyPreparationSources(ctx, out.Preparation.inputs, version, out.Command.Recipe); e != nil {
		return outputExpectation{}, e
	}
	release, e := s.acquireScan(ctx)
	if e != nil {
		return outputExpectation{}, e
	}
	defer release()
	request, e := out.Command.ContainerRequest()
	if e != nil {
		return outputExpectation{}, e
	}
	requestBytes, e := out.Command.RequestBytes()
	if e != nil {
		return outputExpectation{}, e
	}
	out.Expected = wire.ResultExpectation{Request: request, RequestBytes: requestBytes, BaseFiles: wire.ApprovedBaseFiles()}
	file, size, e := s.Datasets.OpenPublishedSource(ctx, version, min(s.policy().ScanMaximumBytes, datawire.MaximumBytesCeiling))
	if e != nil {
		return outputExpectation{}, e
	}
	defer file.Close()
	if size != out.Preparation.inputs.Heldout.SourceBytes {
		return outputExpectation{}, fail("MODEL_PREPARATION_INPUT_CHANGED")
	}
	scanner := bufio.NewScanner(io.LimitReader(contextReader{ctx: ctx, reader: file}, size+1))
	scanner.Buffer(make([]byte, 1024), int(request.DataLimits.MaximumLineBytes)+1)
	for scanner.Scan() {
		if int64(len(out.Expected.Cases)) >= out.Preparation.inputs.Heldout.RowCount {
			return outputExpectation{}, fail("MODEL_PREPARATION_INPUT_CHANGED")
		}
		row, e := datawire.DecodeRecord(scanner.Bytes(), request.DataLimits)
		if e != nil || !validLabel(row.Completion) {
			return outputExpectation{}, fail("MODEL_PREPARATION_INPUT_CHANGED")
		}
		out.Expected.Cases = append(out.Expected.Cases, wire.ExpectedCase{SourceID: row.SourceID, PromptSHA256: hash([]byte(row.Prompt)), Label: row.Completion})
	}
	if scanner.Err() != nil || int64(len(out.Expected.Cases)) != out.Preparation.inputs.Heldout.RowCount {
		return outputExpectation{}, fail("MODEL_PREPARATION_INPUT_CHANGED")
	}
	var tokenizer struct {
		ChatTemplate string `json:"chat_template"`
	}
	for _, asset := range out.Expected.BaseFiles {
		if asset.Path == "tokenizer_config.json" {
			raw, e := io.ReadAll(io.LimitReader(contextReader{ctx: ctx, reader: tokenizerFile}, asset.SizeBytes+1))
			if e != nil || int64(len(raw)) != asset.SizeBytes || hash(raw) != asset.SHA256 || json.Unmarshal(raw, &tokenizer) != nil || tokenizer.ChatTemplate == "" {
				return outputExpectation{}, fail("MODEL_PREPARATION_INPUT_CHANGED")
			}
		}
	}
	out.Expected.ChatTemplateSHA256 = hash([]byte(tokenizer.ChatTemplate))
	var config struct {
		Tied bool `json:"tie_word_embeddings"`
	}
	for _, asset := range out.Expected.BaseFiles {
		if asset.Path == "config.json" {
			raw, e := io.ReadAll(io.LimitReader(contextReader{ctx: ctx, reader: configurationFile}, asset.SizeBytes+1))
			if e != nil || int64(len(raw)) != asset.SizeBytes || hash(raw) != asset.SHA256 || json.Unmarshal(raw, &config) != nil || !config.Tied {
				return outputExpectation{}, fail("MODEL_PREPARATION_INPUT_CHANGED")
			}
		}
	}
	out.Report, e = wire.DecodePreparedResult(metadata, out.Expected)
	if e != nil {
		return outputExpectation{}, e
	}
	return out, nil
}

func (s *Service) bindOutputExpectation(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, p Preparation, c wire.Command, expected outputExpectation) error {
	sha, e := c.Digest()
	if e != nil {
		return e
	}
	previous, e := expected.Command.Digest()
	if e != nil {
		return e
	}
	if sha != previous || p.InputSHA256 != expected.Preparation.InputSHA256 {
		return fail("MODEL_PREPARATION_ATTEMPT_STALE")
	}
	sources, _, e := s.preparationSourcesWithin(ctx, tx, w, CreateRequest{NodeID: p.CurrentNodeID, TrainingJobID: p.TrainingJobID, HeldoutID: p.HeldoutID})
	if e != nil {
		return e
	}
	if sources.digest() != p.InputSHA256 {
		return fail("MODEL_PREPARATION_INPUT_CHANGED")
	}
	var sequence int64
	if e = preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT "LAST_EVENT_SEQUENCE" FROM "MODEL_PREPARATION_ATTEMPTS" WHERE "ID"=? FOR UPDATE`), "SELECT last_event_sequence FROM model_preparation_attempts WHERE id=$1 FOR UPDATE", `SELECT last_event_sequence FROM model_preparation_attempts WHERE id=? FOR UPDATE`, c.AttemptID)).Scan(&sequence); e != nil {
		return e
	}
	if sequence != expected.Report.EventCount {
		return fail("MODEL_PREPARATION_EVENT_ORDER_CONFLICT")
	}
	return nil
}
