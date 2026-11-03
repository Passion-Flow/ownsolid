package main

import (
	"errors"
	"flag"
	"path/filepath"
)

// Admission runs before configuration or database access, including operator commands.
func validatePolicyEnvironmentFlags(command string, flags *flag.FlagSet, path string) error {
	supplied := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "env-file" {
			supplied = true
		}
	})
	trainingOperator := command == "export-training-files" || command == "import-training-files" || command == "verify-training-files" || command == "initialize-training-store" || command == "check-training-store-initialization" || command == "record-training-store-initialization"
	preparationOperator := command == "export-preparation-files" || command == "import-preparation-files" || command == "verify-preparation-files" || command == "initialize-preparation-store" || command == "check-preparation-store-initialization" || command == "record-preparation-store-initialization"
	datasetOperator := command == "export-dataset-files" || command == "import-dataset-files" || command == "verify-dataset-store" || command == "initialize-dataset-store"
	if supplied && ((!datasetOperator && !trainingOperator && !preparationOperator && !mvpDatabaseCommand(command) && command != "serve") || path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path) {
		return errors.New("POLICY_ENVIRONMENT_FLAG_INVALID")
	}
	return nil
}
