package v1

import (
	"encoding/json"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"strings"
	"testing"
)

func validCommand() Command {
	r := validRequest()
	image := "sha256:" + strings.Repeat("a", 64)
	configuration := DefaultConfiguration()
	digest, _ := configuration.Digest()
	return Command{SchemaVersion: 1, Scope: Scope{TenantID: r.TenantID, WorkspaceID: r.WorkspaceID, JobID: r.JobID}, AttemptID: r.AttemptID, AttemptNumber: 1, ModelID: r.DatasetVersionID, DatasetID: r.DatasetVersionID, DatasetVersionID: r.DatasetVersionID, DatasetManifestSHA256: r.DatasetManifestSHA256, ModelCatalogSHA256: r.ModelCatalogSHA256, GPUUUID: "GPU-00000000-0000-4000-8000-000000000001", Configuration: configuration, ConfigurationSHA256: digest, Recipe: ExecutionRecipe{SchemaVersion: 1, WorkerVersion: WorkerVersion, ImageReference: "projectone/training@sha256:" + strings.Repeat("b", 64), Identity: ExecutionIdentity{Profile: ProductionProfile, Python: PythonVersion, WorkerSourceSHA256: strings.Repeat("c", 64), ImageID: &image}, Resources: DefaultResources(), DataLimits: r.DataLimits}, Inputs: InputInventory{Train: datawire.Asset{Path: "train.jsonl", SizeBytes: 128, SHA256: strings.Repeat("d", 64)}, Validation: datawire.Asset{Path: "validation.jsonl", SizeBytes: 128, SHA256: strings.Repeat("e", 64)}}}
}
func TestTypedProductionCommandIdentityInputsAndStrictNulls(t *testing.T) {
	c := validCommand()
	raw, _ := json.Marshal(c)
	if _, e := DecodeCommand(raw); e != nil {
		t.Fatal(e)
	}
	for name, change := range map[string]func(*Command){"verification_device": func(c *Command) { c.Configuration.Device = VerificationProfile }, "configuration_digest": func(c *Command) { c.ConfigurationSHA256 = strings.Repeat("f", 64) }, "foreign_path": func(c *Command) { c.Inputs.Train.Path = "../train.jsonl" }, "identical_partitions": func(c *Command) { c.Inputs.Validation.SHA256 = c.Inputs.Train.SHA256 }, "mutable_image": func(c *Command) { c.Recipe.ImageReference = "projectone/training:latest" }, "missing_image": func(c *Command) { c.Recipe.Identity.ImageID = nil }, "zero_attempt": func(c *Command) { c.AttemptNumber = 0 }} {
		t.Run(name, func(t *testing.T) {
			c := validCommand()
			change(&c)
			if c.Validate() == nil {
				t.Fatal("unsafe command accepted")
			}
		})
	}
	for _, bad := range []string{strings.Replace(string(raw), `"resume":null`, `"Resume":null`, 1), strings.Replace(string(raw), `"resume":null`, `"resume":null,"resume":null`, 1), strings.Replace(string(raw), `"inputs":{`, `"inputs":null,"unknown":{`, 1)} {
		if _, e := DecodeCommand([]byte(bad)); e == nil {
			t.Fatal("noncanonical command accepted")
		}
	}
}
