package main

import (
	"errors"
	"flag"
	"path/filepath"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/storeinitialization"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

const configuredModelInitializationCommand = "initialize-configured-model-store"

func validateConfiguredModelInitializationFlags(flags *flag.FlagSet, configuration, root string, uid, gid int, confirmed bool) error {
	allowed := map[string]bool{"config": true, "model-store-root": true, "owner-uid": true, "owner-gid": true, "confirm-empty-model-store": true}
	valid := configuration != "" && root != "" && uid > 0 && gid > 0 && confirmed
	flags.Visit(func(f *flag.Flag) {
		if !allowed[f.Name] {
			valid = false
		}
	})
	if !valid {
		return errors.New("CONFIGURED_MODEL_INITIALIZATION_ARGUMENT_INVALID")
	}
	return nil
}

func validateConfiguredModelRecoveryFlags(flags *flag.FlagSet, command, configuration, root string, uid, gid int, reason string) error {
	allowed := map[string]bool{"config": true, "model-store-root": true, "owner-uid": true, "owner-gid": true}
	valid := configuration != "" && root != "" && uid > 0 && gid > 0
	switch command {
	case storeinitialization.CheckModelCommand:
		valid = valid && reason == ""
	case storeinitialization.RecordModelCommand:
		allowed["reason"] = true
		valid = valid && modelpreparations.ValidOperatorReason(reason)
	default:
		valid = false
	}
	flags.Visit(func(f *flag.Flag) {
		if !allowed[f.Name] {
			valid = false
		}
	})
	if !valid {
		return errors.New("CONFIGURED_MODEL_INITIALIZATION_ARGUMENT_INVALID")
	}
	return nil
}
func initializeConfiguredModelStore(c config.Config, root string, uid, gid int, confirmed bool) error {
	if !confirmed {
		return errors.New("EMPTY_MODEL_STORE_CONFIRMATION_REQUIRED")
	}
	if uid <= 0 || gid <= 0 {
		return errors.New("MODEL_STORE_RUNTIME_OWNER_INVALID")
	}
	if c.Models.ArtifactRoot == "" || root != c.Models.ArtifactRoot || !filepath.IsAbs(root) || filepath.Clean(root) != root || root == string(filepath.Separator) {
		return errors.New("MODEL_STORE_CONFIGURED_ROOT_REQUIRED")
	}
	return models.InitializeStore(root, uid, gid)
}
