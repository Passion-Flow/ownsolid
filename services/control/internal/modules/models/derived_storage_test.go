package models

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

func derivedStorageCatalog() wire.DerivedCatalog {
	id, artifact := uuid.NewString(), uuid.NewString()
	files := wire.ApprovedBaseFiles()
	for i := range files {
		if files[i].Path == "model.safetensors" {
			files[i].SizeBytes = 1192135096
			files[i].SHA256 = strings.Repeat("c", 64)
		}
	}
	return wire.DerivedCatalog{SchemaVersion: 1, Kind: "merged_lora", Scope: wire.Scope{TenantID: id, WorkspaceID: id, PreparationID: id}, ModelID: artifact, BaseModelID: id, BaseCatalogSHA256: wire.ApprovedModelCatalogSHA256, AttemptID: id, ArtifactID: artifact, ReportSHA256: strings.Repeat("d", 64), RecipeSHA256: wire.RecipeDigest(), BaseRepository: wire.ApprovedBaseRepository, BaseRevision: wire.ApprovedBaseRevision, License: wire.ApprovedBaseLicense, Format: "safetensors", Architecture: wire.ApprovedBaseArchitecture, ModelType: wire.ApprovedBaseModelType, Precision: "BF16", MaximumContextTokens: wire.MaximumContextTokens, Files: files}
}
func derivedTestStore(t *testing.T) *Service {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	// Only this newly created synthetic test tree is made writable for test cleanup.
	t.Cleanup(func() {
		filepath.WalkDir(root, func(path string, entry os.DirEntry, e error) error {
			if e == nil && entry.IsDir() {
				os.Chmod(path, 0700)
			}
			return nil
		})
	})
	return &Service{RootDirectory: root}
}
func TestDerivedStorageRejectsMissingSourceInvalidPolicyCancelledAndIncompleteCopies(t *testing.T) {
	s := derivedTestStore(t)
	c := derivedStorageCatalog()
	ctx := context.Background()
	if _, e := s.PrepareDerivedFiles(ctx, c, nil, 1048576); e == nil {
		t.Fatal("missing source accepted")
	}
	filePath := filepath.Join(t.TempDir(), "synthetic-source")
	if e := os.WriteFile(filePath, []byte("synthetic source"), 0600); e != nil {
		t.Fatal(e)
	}
	file, e := os.Open(filePath)
	if e != nil {
		t.Fatal(e)
	}
	defer file.Close()
	sources := map[string]*os.File{}
	for _, asset := range c.Files {
		sources[asset.Path] = file
	}
	if _, e = s.PrepareDerivedFiles(ctx, c, sources, 0); e == nil {
		t.Fatal("unbounded disk reserve accepted")
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if _, e = s.PrepareDerivedFiles(cancelled, c, sources, 1048576); e == nil {
		t.Fatal("cancelled copy accepted")
	}
	for i := 0; i < 2; i++ {
		if _, e = s.PrepareDerivedFiles(ctx, c, sources, 1048576); e == nil {
			t.Fatal("wrong source bytes published")
		}
	}
	if _, e = os.Stat(filepath.Join(s.RootDirectory, derivedDirectory(c))); !os.IsNotExist(e) {
		t.Fatal("incomplete source became canonical model")
	}
	raw, e := os.ReadFile(filePath)
	if e != nil || string(raw) != "synthetic source" {
		t.Fatal("source bytes changed")
	}
	if e = s.VerifyDerivedFiles(ctx, c); e == nil {
		t.Fatal("missing derived copy verified")
	}
}
func TestDerivedStorageRefusesUnknownCanonicalDirectoryAndForeignLock(t *testing.T) {
	for _, mode := range []string{"unknown_directory", "hardlinked_lock"} {
		t.Run(mode, func(t *testing.T) {
			s := derivedTestStore(t)
			c := derivedStorageCatalog()
			os.Mkdir(filepath.Join(s.RootDirectory, "derived"), 0700)
			if mode == "unknown_directory" {
				os.Mkdir(filepath.Join(s.RootDirectory, derivedDirectory(c)), 0555)
			} else {
				path := filepath.Join(s.RootDirectory, "derived", ".lock-"+c.ModelID)
				os.WriteFile(path, nil, 0600)
				os.Link(path, path+".foreign")
			}
			filePath := filepath.Join(t.TempDir(), "input")
			os.WriteFile(filePath, []byte("x"), 0600)
			file, _ := os.Open(filePath)
			defer file.Close()
			sources := map[string]*os.File{}
			for _, asset := range c.Files {
				sources[asset.Path] = file
			}
			if _, e := s.PrepareDerivedFiles(context.Background(), c, sources, 1048576); e == nil {
				t.Fatal("unknown/foreign state adopted")
			}
		})
	}
}
