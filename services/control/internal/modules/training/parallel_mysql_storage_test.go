//go:build repository_integration && training_artifact_integration

package training_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// This explicit native-Linux profile does not relabel historical tmpfs file
// checks as durable storage. It records the actual test/store filesystem paths.
func TestParallelMySQLTrainingDurableRuntimeHandoff(t *testing.T) {
	if _, inherited := os.LookupEnv("GOTMPDIR"); inherited {
		t.Fatal("TRAINING_RUNTIME_GOTMPDIR_MUST_BE_UNSET")
	}
	parent := os.Getenv("TMPDIR")
	if !filepath.IsAbs(parent) || filepath.Clean(parent) != parent {
		t.Fatal("TRAINING_PRIVATE_DURABLE_TMPDIR_REQUIRED")
	}
	trainingRuntimeAcceptedHandoff(t, func(t *testing.T, g trainingRuntimeStory) {
		paths := map[string]string{
			"testing_tempdir": t.TempDir(),
			"datasets":        g.owner.Config.Datasets.ArtifactRoot,
			"models":          g.owner.Config.Models.ArtifactRoot,
			"training":        g.owner.Config.Training.ArtifactRoot,
		}
		for owner, path := range paths {
			resolved, e := filepath.EvalSymlinks(path)
			relative, relError := filepath.Rel(parent, path)
			info, statError := os.Stat(path)
			if e != nil || resolved != path || relError != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || statError != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
				t.Fatal("TRAINING_DURABLE_STORE_PATH_REQUIRED", owner)
			}
			raw, e := exec.CommandContext(t.Context(), "/usr/bin/findmnt", "--json", "--target", path, "--output", "SOURCE,TARGET,FSTYPE").Output()
			var observation struct {
				Filesystems []struct {
					Source string `json:"source"`
					Target string `json:"target"`
					FSType string `json:"fstype"`
				} `json:"filesystems"`
			}
			if e != nil || json.Unmarshal(raw, &observation) != nil || len(observation.Filesystems) != 1 || observation.Filesystems[0].FSType != "ext4" || observation.Filesystems[0].Source == "" {
				t.Fatal("TRAINING_ACTUAL_EXT4_RUNTIME_REQUIRED", owner)
			}
			value := struct {
				Owner, Path, Source, Target, FSType string
			}{owner, path, observation.Filesystems[0].Source, observation.Filesystems[0].Target, observation.Filesystems[0].FSType}
			encoded, e := json.Marshal(value)
			if e != nil {
				t.Fatal(e)
			}
			t.Logf("DURABLE_TRAINING_RUNTIME_PATH %s", encoded)
		}
	})
}
