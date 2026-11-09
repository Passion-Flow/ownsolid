//go:build repository_integration

package models_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	lifecycle "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func verifyHTTPDerivedFiles(t *testing.T, x modelReadFixture, catalog lifecycle.DerivedCatalog, source *accessDerivedSource, detail map[string]any) {
	t.Helper()
	raw, err := json.Marshal(detail["manifest"])
	if err != nil {
		t.Fatal(err)
	}
	readback, err := lifecycle.DecodeDerivedCatalog(raw)
	sum := sha256.Sum256(source.report)
	if err != nil || !reflect.DeepEqual(readback, catalog) || !reflect.DeepEqual(readback.Files, source.files) ||
		readback.ReportSHA256 != hex.EncodeToString(sum[:]) {
		t.Fatal("Admin lineage detail lost the retained report or file inventory", err)
	}
	catalogSHA, err := readback.Digest()
	if err != nil || detail["catalog_digest"] != catalogSHA {
		t.Fatal("Admin detail changed the published catalog digest", err)
	}

	reopened := *x.s
	if err = reopened.VerifyDerivedFiles(x.ctx, readback); err != nil {
		t.Fatal("verify published artifact in a fresh model service", err)
	}
	x.s = &reopened
	accessPrepare(t, x, readback, true)

	// Check the whole-file hash a caller obtains by assembling the real ranges.
	command := accessCommand(readback)
	var bytes int64
	var ranges int
	for _, asset := range readback.Files {
		whole := sha256.New()
		err = x.f.Store.Within(x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
			for offset := int64(0); offset < asset.SizeBytes; {
				request := accessRequest(readback, asset.Path, offset)
				download, err := reopened.OpenDerivedTransferWithin(ctx, q, x.f.Tenant, x.f.Workspace, command, request)
				if err != nil {
					return err
				}
				chunk := sha256.New()
				n, err := io.Copy(io.MultiWriter(whole, chunk), io.NewSectionReader(download.File, download.Offset, download.Size))
				closeErr := download.File.Close()
				if err != nil || closeErr != nil || download.Offset != offset || n != request.Length ||
					download.SHA256 != hex.EncodeToString(chunk.Sum(nil)) {
					return fmt.Errorf("invalid retained model range for %s at %d", asset.Path, offset)
				}
				offset += n
				bytes += n
				ranges++
			}
			return nil
		})
		if err != nil || hex.EncodeToString(whole.Sum(nil)) != asset.SHA256 {
			t.Fatalf("transferred %s does not match the Admin catalog: %v", asset.Path, err)
		}
	}

	// Corrupt one byte of the isolated copy, leaving the producer untouched.
	path := filepath.Join(reopened.RootDirectory, "derived", readback.ModelID, "config.json")
	original, err := os.ReadFile(path)
	if err != nil || len(original) == 0 {
		t.Fatal("published config fixture", err)
	}
	original[0] ^= 1
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0444); err != nil {
		t.Fatal(err)
	}
	if err = reopened.VerifyDerivedFiles(x.ctx, readback); err == nil || err.Error() != "MODEL_DERIVED_STORAGE_INVALID" {
		t.Fatal("expected retained storage integrity refusal after changing a file", err)
	}
	t.Logf("Verified retained CPU report %s and catalog %s across %d files, %d bytes and %d ranges", readback.ReportSHA256, catalogSHA, len(readback.Files), bytes, ranges)
}
