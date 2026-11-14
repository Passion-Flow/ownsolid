package main

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/storeinitialization"
	"strconv"
	"testing"

	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func TestConfiguredModelInitializationActualDispatcherRootBindingAndPreservation(t *testing.T) {
	original := os.Args
	defer func() { os.Args = original }()
	private := t.TempDir()
	if os.Chmod(private, 0700) != nil {
		t.Fatal("private configuration root")
	}
	root := filepath.Join(private, "models-nondefault")
	other := filepath.Join(private, "unrelated-empty")
	os.Mkdir(root, 0755)
	os.Mkdir(other, 0755)
	c := config.Config{}
	c.Server.ListenAddress = "127.0.0.1:19443"
	c.Server.PortalOrigin = "https://portal.example.invalid"
	c.Server.AdminOrigin = "https://admin.example.invalid"
	c.Database.MaxOpenConnections = 4
	c.Database.MaxIdleConnections = 1
	c.Database.StatementTimeoutSeconds = 5
	c.SMTP.Host = "smtp.example.invalid"
	c.SMTP.Port = 587
	c.SMTP.FromAddress = "platform@example.invalid"
	c.SMTP.TLSMode = "starttls_required"
	c.SMTP.ConnectTimeoutSeconds = 5
	c.SMTP.OperationTimeoutSeconds = 20
	c.Models.ArtifactRoot = root
	raw, e := yaml.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(private, "control.yaml")
	if os.WriteFile(path, raw, 0600) != nil {
		t.Fatal("private config")
	}
	args := []string{"control", configuredModelInitializationCommand, "--config", path, "--model-store-root", root, "--owner-uid", strconv.Itoa(os.Geteuid()), "--owner-gid", strconv.Itoa(os.Getegid()), "--confirm-empty-model-store=true"}
	os.Args = append([]string{}, args...)
	os.Args[5] = other
	if e := run(); e == nil || e.Error() != "MODEL_STORE_CONFIGURED_ROOT_REQUIRED" {
		t.Fatal("explicit root overrode configuration", e)
	}
	info, _ := os.Stat(other)
	if info.Mode().Perm() != 0755 {
		t.Fatal("wrong root mutated")
	}
	os.Args = args
	if e := run(); e != nil {
		t.Fatal("actual configured initialization without DB/auth/mail/frontend assets", e)
	}
	info, _ = os.Stat(root)
	if info.Mode().Perm() != 0700 {
		t.Fatal("private runtime root")
	}
	if e := run(); e != nil {
		t.Fatal("same existing empty root", e)
	}
	sentinel := filepath.Join(root, "retained-data")
	content := []byte("PRESERVE_SYNTHETIC_DATA")
	os.WriteFile(sentinel, content, 0600)
	if e := run(); e == nil || e.Error() != "MODEL_STORE_NOT_EMPTY" {
		t.Fatal("populated root adopted", e)
	}
	retained, _ := os.ReadFile(sentinel)
	if !bytes.Equal(retained, content) {
		t.Fatal("retained data changed")
	}
	if e := initializeConfiguredModelStore(c, root, os.Geteuid(), os.Getegid(), false); e == nil {
		t.Fatal("missing empty confirmation")
	}
	if e := initializeConfiguredModelStore(c, root, 0, os.Getegid(), true); e == nil {
		t.Fatal("root runtime identity")
	}
	link := filepath.Join(private, "linked-models")
	os.Symlink(root, link)
	c.Models.ArtifactRoot = link
	if e := initializeConfiguredModelStore(c, link, os.Geteuid(), os.Getegid(), true); e == nil {
		t.Fatal("linked configured root")
	}
	missing := filepath.Join(private, "missing-models")
	c.Models.ArtifactRoot = missing
	if e := initializeConfiguredModelStore(c, missing, os.Geteuid(), os.Getegid(), true); e == nil {
		t.Fatal("missing root silently created")
	}
	if _, e := os.Stat(missing); !os.IsNotExist(e) {
		t.Fatal("missing mount appeared")
	}
}
func TestConfiguredModelInitializationClosedFlagsBeforeConfigurationOrFilesystem(t *testing.T) {
	original := os.Args
	defer func() { os.Args = original }()
	base := []string{"control", configuredModelInitializationCommand, "--config", "must-not-open", "--model-store-root", "/synthetic-unowned-models", "--owner-uid", "10031", "--owner-gid", "10032"}
	for _, extra := range [][]string{nil, {"--confirm-empty-model-store=false"}, {"--confirm-empty-model-store=true", "--runtime-config", "SYNTHETIC_PRIVATE_UNUSED"}, {"--confirm-empty-model-store=true", "--dataset-store-root", "/synthetic-unowned-datasets"}} {
		os.Args = append(append([]string{}, base...), extra...)
		if e := run(); e == nil || e.Error() != "CONFIGURED_MODEL_INITIALIZATION_ARGUMENT_INVALID" {
			t.Fatal("unsafe flags reached config/FS", e)
		}
	}
	os.Args = append(append([]string{}, base...), "--confirm-empty-model-store=true", "unowned-positional-value")
	if e := run(); e == nil || e.Error() != "OPERATOR_ARGUMENT_INVALID" {
		t.Fatal("unused positional value")
	}
}

func TestConfiguredMySQLModelRecoveryFlagsAreClosedAndRequireExplicitReason(t *testing.T) {
	for _, tc := range []struct {
		command string
		extras  []string
		valid   bool
	}{
		{storeinitialization.CheckModelCommand, nil, true},
		{storeinitialization.CheckModelCommand, []string{"--reason", "Synthetic explicit operator reason"}, false},
		{storeinitialization.CheckModelCommand, []string{"--confirm-empty-model-store=false"}, false},
		{storeinitialization.RecordModelCommand, nil, false},
		{storeinitialization.RecordModelCommand, []string{"--reason", "Synthetic explicit operator reason"}, true},
		{storeinitialization.RecordModelCommand, []string{"--reason", "Synthetic explicit operator reason", "--dataset-store-root", "/synthetic/foreign-root"}, false},
	} {
		f := flag.NewFlagSet(tc.command, flag.ContinueOnError)
		configuration := f.String("config", "", "fixture")
		root := f.String("model-store-root", "", "fixture")
		uid := f.Int("owner-uid", -1, "fixture")
		gid := f.Int("owner-gid", -1, "fixture")
		reason := f.String("reason", "", "fixture")
		f.Bool("confirm-empty-model-store", false, "fixture")
		f.String("dataset-store-root", "", "fixture")
		args := []string{"--config", "/synthetic/private-control", "--model-store-root", "/synthetic/model-root", "--owner-uid", "10031", "--owner-gid", "10032"}
		if e := f.Parse(append(args, tc.extras...)); e != nil {
			t.Fatal(e)
		}
		e := validateConfiguredModelRecoveryFlags(f, tc.command, *configuration, *root, *uid, *gid, *reason)
		if (e == nil) != tc.valid {
			t.Fatal("closed recovery flags", tc.command, tc.extras, e)
		}
	}
}
