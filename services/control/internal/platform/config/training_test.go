package config

import (
	"strings"
	"testing"
)

func TestTrainingRuntimeArtifactIdentityAndResourcePolicy(t *testing.T) {
	if e := DefaultTrainingPolicy().Validate(); e != nil {
		t.Fatal(e)
	}
	c := TrainingConfiguration{ArtifactRoot: "/synthetic/training", ImageReference: "projectone/training@sha256:" + strings.Repeat("a", 64), ImageID: "sha256:" + strings.Repeat("b", 64), WorkerSourceSHA256: strings.Repeat("c", 64)}
	if e := c.Validate("/synthetic/models", "/synthetic/datasets"); e != nil {
		t.Fatal(e)
	}
	if (TrainingConfiguration{}).Enabled() || (TrainingConfiguration{}).Validate() != nil {
		t.Fatal("absent training configuration not disabled")
	}
	for _, change := range []func(*TrainingConfiguration){func(c *TrainingConfiguration) { c.ImageID = "" }, func(c *TrainingConfiguration) { c.ImageReference = "projectone/training:latest" }, func(c *TrainingConfiguration) { c.WorkerSourceSHA256 = "invalid" }, func(c *TrainingConfiguration) { c.ArtifactRoot = "/synthetic/models/nested" }, func(c *TrainingConfiguration) { c.ArtifactRoot = "relative" }} {
		copy := c
		change(&copy)
		if copy.Validate("/synthetic/models") == nil {
			t.Fatal("unsafe artifact configuration accepted")
		}
	}
	for _, change := range []func(*TrainingPolicy){func(p *TrainingPolicy) { p.MaxAttempts = 0 }, func(p *TrainingPolicy) { p.Resources.MemoryBytes = 0 }, func(p *TrainingPolicy) { p.MinimumDriverMajor = 579 }, func(p *TrainingPolicy) { p.MinimumComputeMajor = 7 }, func(p *TrainingPolicy) { p.MaxActivePerWorkspace = p.MaxJobsPerWorkspace + 1 }, func(p *TrainingPolicy) { p.MinimumFreeBytes = p.Resources.MaximumOutputBytes - 1 }} {
		p := DefaultTrainingPolicy()
		change(&p)
		if p.Validate() == nil {
			t.Fatal("unsafe training policy accepted")
		}
	}
}

func TestTrainingUploadDeadlineBoundsAndExplicitOverride(t *testing.T) {
	for _, seconds := range []int{0, 301} {
		p := DefaultTrainingPolicy()
		p.UploadSeconds = seconds
		if p.Validate() == nil {
			t.Fatal("unsafe upload deadline accepted")
		}
	}
	for _, seconds := range []int{1, 17, 300} {
		p := DefaultTrainingPolicy()
		p.UploadSeconds = seconds
		if e := p.Validate(); e != nil {
			t.Fatal("supported explicit upload deadline rejected", e)
		}
	}
}
