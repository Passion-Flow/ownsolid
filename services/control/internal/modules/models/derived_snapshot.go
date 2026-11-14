package models

import (
	"context"
	"os"
	"path/filepath"

	"github.com/Passion-Flow/ownsolid/api/agent/modelartifact"
)

func hasBaseSnapshotModels(inventory FileSnapshot) bool {
	for _, model := range inventory.Models {
		if model.Kind == "" {
			return true
		}
	}
	return false
}
func hasBaseReadySnapshotModels(inventory FileSnapshot) bool {
	for _, model := range inventory.Models {
		if model.Kind == "" && model.State == "ready" {
			return true
		}
	}
	return false
}
func verifySnapshotModel(ctx context.Context, source string, model SnapshotModel) error {
	switch model.Kind {
	case "":
		if model.DerivedCatalog != nil {
			return fail("MODEL_SNAPSHOT_INVALID")
		}
		return modelartifact.VerifyDirectory(ctx, filepath.Join(source, "objects", model.ID))
	case "merged_lora":
		if model.DerivedCatalog == nil {
			return fail("MODEL_SNAPSHOT_INVALID")
		}
		return (&Service{RootDirectory: source}).VerifyDerivedFiles(ctx, *model.DerivedCatalog)
	default:
		return fail("MODEL_SNAPSHOT_INVALID")
	}
}
func copySnapshotModel(ctx context.Context, source, destination string, model SnapshotModel) error {
	if model.Kind == "" && model.DerivedCatalog == nil {
		return copyModel(ctx, filepath.Join(source, "objects", model.ID), filepath.Join(destination, "objects", model.ID))
	}
	if model.Kind != "merged_lora" || model.DerivedCatalog == nil {
		return fail("MODEL_SNAPSHOT_INVALID")
	}
	c := *model.DerivedCatalog
	root, e := openStore(source)
	if e != nil {
		return e
	}
	defer root.Close()
	if e = verifyDerivedDirectory(ctx, root, c); e != nil {
		return e
	}
	inputs := map[string]*os.File{}
	defer func() {
		for _, file := range inputs {
			file.Close()
		}
	}()
	for _, asset := range c.Files {
		file, e := derivedRegular(root, filepath.Join(derivedDirectory(c), asset.Path), asset.SizeBytes, 0444)
		if e != nil {
			return e
		}
		inputs[asset.Path] = file
	}
	// Only this owning path admits verified read-only snapshot FDs; public admission still requires private source modes.
	_, e = (&Service{RootDirectory: destination}).prepareDerivedFiles(ctx, c, inputs, 1048576, true)
	return e
}
