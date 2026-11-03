package modelpreparations

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

func prepareAttempt(ctx context.Context, config Config, j Journal, download FileDownload) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if config.Bind(j.Command) != nil || download == nil {
		return fail("MODEL_PREPARATION_AGENT_RECIPE_MISMATCH")
	}
	parent := config.RootDirectory
	for _, name := range []string{"preparations", j.Command.Scope.TenantID, j.Command.Scope.WorkspaceID, j.Command.Scope.PreparationID} {
		parent = filepath.Join(parent, name)
		if e := rootDirectory(parent); e != nil {
			return e
		}
	}
	attempt := attemptPath(config, j)
	if os.Mkdir(attempt, 0700) != nil {
		return fail("MODEL_PREPARATION_AGENT_PARTIAL_INPUT_RETAINED")
	}
	inputs, adapter, output := filepath.Join(attempt, "inputs"), filepath.Join(attempt, "inputs/adapter"), filepath.Join(attempt, "output")
	for _, path := range []string{inputs, adapter, output} {
		if os.Mkdir(path, 0700) != nil {
			return fail("MODEL_PREPARATION_AGENT_IO_FAILED")
		}
	}
	type source struct {
		kind  string
		asset datawire.Asset
		path  string
	}
	sources := []source{{"heldout", j.Command.Inputs.Heldout, filepath.Join(inputs, "heldout.jsonl")}}
	for _, asset := range j.Command.Inputs.AdapterFiles {
		sources = append(sources, source{"adapter", asset, filepath.Join(adapter, asset.Path)})
	}
	for _, item := range sources {
		if e := download(ctx, item.kind, item.asset, item.path); e != nil {
			return e
		}
		file, e := verifiedAsset(ctx, item.path, item.asset, os.Geteuid())
		if e != nil {
			return e
		}
		file.Close()
	}
	request, e := j.Command.RequestBytes()
	if e != nil {
		return e
	}
	if e = newPrivateFile(filepath.Join(attempt, "request.json"), request, os.Geteuid(), os.Getegid()); e != nil {
		return e
	}
	marker := map[string]any{"schema_version": 1, "tenant_id": j.Command.Scope.TenantID, "workspace_id": j.Command.Scope.WorkspaceID, "preparation_id": j.Command.Scope.PreparationID, "attempt_id": j.Command.AttemptID, "training_job_id": j.Command.TrainingJobID, "training_attempt_id": j.Command.TrainingAttemptID, "heldout_id": j.Command.HeldoutID}
	body, _ := json.Marshal(marker)
	if e = newPrivateFile(filepath.Join(attempt, "projectone-preparation.json"), body, os.Geteuid(), os.Getegid()); e != nil {
		return e
	}
	p := config.Policy()
	count := 0
	e = filepath.WalkDir(attempt, func(path string, entry os.DirEntry, e error) error {
		if e != nil {
			return fail("MODEL_PREPARATION_AGENT_IO_FAILED")
		}
		count++
		if count > MaximumPreparedInputEntries || entry.Type()&os.ModeSymlink != 0 {
			return fail("MODEL_PREPARATION_AGENT_PATH_INVALID")
		}
		if os.Chown(path, p.UserID, p.GroupID) != nil {
			return fail("MODEL_PREPARATION_AGENT_IO_FAILED")
		}
		if entry.IsDir() {
			return syncDirectory(path)
		}
		return nil
	})
	if e != nil {
		return e
	}
	return syncDirectory(attempt)
}

// Expectations are derived from protected downloaded inputs before the container starts.
func resultExpectation(ctx context.Context, config Config, j Journal) (wire.ResultExpectation, error) {
	var out wire.ResultExpectation
	var e error
	out.Request, e = j.Command.ContainerRequest()
	if e != nil {
		return out, e
	}
	out.RequestBytes, e = j.Command.RequestBytes()
	if e != nil {
		return out, e
	}
	out.BaseFiles = wire.ApprovedBaseFiles()
	input := j.Command.Inputs.Heldout
	file, e := verifiedAsset(ctx, filepath.Join(attemptPath(config, j), "inputs/heldout.jsonl"), input, config.Policy().UserID)
	if e != nil {
		return out, e
	}
	defer file.Close()
	limits := j.Command.Recipe.DataLimits
	scanner := bufio.NewScanner(contextReader{ctx, file})
	scanner.Buffer(make([]byte, 1024), int(limits.MaximumLineBytes)+1)
	labels, prompts := map[string]bool{}, map[string]bool{}
	for scanner.Scan() {
		if int64(len(out.Cases)) >= min(limits.MaximumRows, wire.MaximumRows) {
			return out, fail("MODEL_PREPARATION_HELDOUT_INVALID")
		}
		row, e := datawire.DecodeRecord(scanner.Bytes(), limits)
		if e != nil {
			return out, fail("MODEL_PREPARATION_HELDOUT_INVALID")
		}
		valid := false
		for _, label := range wire.Labels() {
			if row.Completion == label {
				valid = true
			}
		}
		prompt := digest([]byte(row.Prompt))
		if !valid || prompts[prompt] {
			return out, fail("MODEL_PREPARATION_HELDOUT_INVALID")
		}
		prompts[prompt] = true
		labels[row.Completion] = true
		out.Cases = append(out.Cases, wire.ExpectedCase{SourceID: row.SourceID, PromptSHA256: prompt, Label: row.Completion})
	}
	if scanner.Err() != nil || int64(len(out.Cases)) < wire.MinimumRows || len(labels) != len(wire.Labels()) {
		return out, fail("MODEL_PREPARATION_HELDOUT_INVALID")
	}
	for _, asset := range out.BaseFiles {
		if asset.Path != "tokenizer_config.json" {
			continue
		}
		path := filepath.Join(config.RootDirectory, "models", j.Command.ModelCatalogSHA256, asset.Path)
		file, e := os.Open(path)
		if e != nil {
			return out, fail("MODEL_PREPARATION_INPUT_INVALID")
		}
		raw, e := io.ReadAll(io.LimitReader(contextReader{ctx, file}, asset.SizeBytes+1))
		file.Close()
		var tokenizer struct {
			ChatTemplate string `json:"chat_template"`
		}
		if e != nil || int64(len(raw)) != asset.SizeBytes || digest(raw) != asset.SHA256 || json.Unmarshal(raw, &tokenizer) != nil || tokenizer.ChatTemplate == "" {
			return out, fail("MODEL_PREPARATION_INPUT_INVALID")
		}
		out.ChatTemplateSHA256 = digest([]byte(tokenizer.ChatTemplate))
	}
	return out, nil
}
