package main

import (
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func TestVerificationStoreExplicitRootIsBindingNeverDisabledSuccess(t *testing.T) {
	commands := []string{"verify-model-store", "verify-dataset-store", "verify-training-files", "verify-preparation-files"}
	for i, command := range commands {
		t.Run(command, func(t *testing.T) {
			c := config.Config{}
			path := "/nondefault/synthetic/retained-store"
			selected := []string{"", "", "", ""}
			selected[i] = path
			check := func() error {
				return validateVerificationStore(c, command, selected[0], selected[1], selected[2], selected[3])
			}
			if check() == nil {
				t.Fatal("disabled store claimed explicit mounted success")
			}
			switch i {
			case 0:
				c.Models.ArtifactRoot = path
			case 1:
				c.Datasets.ArtifactRoot = path
			case 2:
				c.Training.ArtifactRoot = path
			case 3:
				c.ModelPreparation.ArtifactRoot = path
			}
			if e := check(); e != nil {
				t.Fatal("nondefault exact root", e)
			}
			selected[i] = path + "/wrong"
			if check() == nil {
				t.Fatal("root silently overridden")
			}
			selected[i] = path
			selected[(i+1)%len(selected)] = path + "/foreign"
			if check() == nil {
				t.Fatal("other store override accepted")
			}
			if e := validateVerificationStore(config.Config{}, command, "", "", "", ""); e != nil {
				t.Fatal("legacy scope changed", e)
			}
		})
	}
}
