package v1

import (
	"path"
	"testing"
)

func TestSharedDeclaredContainerFilesystemRequestBindsCommand(t *testing.T) {
	c := validCommand()
	r, e := c.ContainerRequest()
	if e != nil {
		t.Fatal(e)
	}
	if r.ScopeDirectory != path.Join(ContainerAttemptsDirectory, c.Scope.JobID, c.AttemptID) || r.ModelDirectory != ContainerModelDirectory || r.TrainFile != path.Join(r.ScopeDirectory, "inputs/train.jsonl") || r.ValidationSHA256 != c.Inputs.Validation.SHA256 || r.ExecutionIdentity.WorkerSourceSHA256 != c.Recipe.Identity.WorkerSourceSHA256 || r.FixtureCatalogFile != nil {
		t.Fatal("declared image ABI or frozen binding drift")
	}
	raw := ExpectedBindings(r, "Qwen/Qwen3-0.6B")
	if e = raw.ValidateFor(r, "Qwen/Qwen3-0.6B"); e != nil {
		t.Fatal(e)
	}
}
