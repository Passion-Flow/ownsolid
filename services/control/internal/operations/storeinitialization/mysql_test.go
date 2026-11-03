package storeinitialization

import (
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func TestMySQLInitializationCommandsKeepActualFormatsAndMissingContextRefuses(t *testing.T) {
	c := config.Config{}
	c.Models.ArtifactRoot = "/synthetic/actual-models"
	c.Datasets.ArtifactRoot = "/synthetic/actual-datasets"
	c.Training.ArtifactRoot = "/synthetic/actual-training"
	c.ModelPreparation.ArtifactRoot = "/synthetic/actual-preparations"
	for _, command := range []string{ConfiguredModelCommand, CheckModelCommand, RecordModelCommand, "initialize-dataset-store", "check-training-store-initialization", "record-preparation-store-initialization"} {
		if !MySQLSupported(command) {
			t.Fatal("owning command missing", command)
		}
		d, e := mysqlDefinition(c, command)
		if e != nil || d.root == "" || d.history == "" {
			t.Fatal("incomplete actual owner binding", command, e)
		}
		status, e := RunMySQL(t.Context(), nil, c, Options{Command: command, Root: d.root, UID: 10031, GID: 10032, ConfirmEmpty: true, Reason: "Synthetic explicit operator reason"})
		if e == nil || status != "" {
			t.Fatal("missing actual installation projected success", command)
		}
	}
	for _, command := range []string{"", "initialize-missing-store", "initialize-model-store", "check-model-store-initialization"} {
		if MySQLSupported(command) {
			t.Fatal("foreign format/command adopted", command)
		}
	}
	if Supported(ConfiguredModelCommand) || Supported(CheckModelCommand) || Supported(RecordModelCommand) {
		t.Fatal("new MySQL proof silently replaced PG operator")
	}
}

func TestMySQLInitializationRootOwnerConfirmationAndReasonFailBeforeFilesystem(t *testing.T) {
	c := config.Config{}
	c.Datasets.ArtifactRoot = "/synthetic/unowned-dataset-root"
	base := Options{Command: "initialize-dataset-store", Root: c.Datasets.ArtifactRoot, UID: 10031, GID: 10032, ConfirmEmpty: true, Reason: "Synthetic explicit operator reason"}
	for _, change := range []func(*Options){func(o *Options) { o.Root += "-foreign" }, func(o *Options) { o.UID = 0 }, func(o *Options) { o.GID = -1 }, func(o *Options) { o.ConfirmEmpty = false }, func(o *Options) { o.Reason = "" }} {
		o := base
		change(&o)
		status, e := RunMySQL(t.Context(), nil, c, o)
		if e == nil || status != "" || e.Error() == "MYSQL_OPERATOR_CONTEXT_REQUIRED" {
			t.Fatal("invalid input reached installation/filesystem", e)
		}
	}
}
