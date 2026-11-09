package install

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

const productionRestorePinFile = "expected-archive.sha256"

func readProductionRestorePin(t *testing.T) string {
	t.Helper()
	delivery := os.Getenv("OWNSOLID_E2E_MODEL_ARCHIVE")
	raw, err := bundle.ReadProtectedConfiguration(filepath.Join(delivery, productionRestorePinFile))
	pin := strings.TrimSuffix(string(raw), "\n")
	if err != nil || !wire.ValidSHA256(pin) {
		t.Fatal("supply the approved production archive SHA-256 in a protected expected-archive.sha256 file")
	}
	return pin
}

func qualifyProductionRestoreMedia(ctx context.Context, archive, expected string, policy bundle.Verification,
	inspect func(context.Context, bundle.Verification, string) (bundle.Result, error),
) (bundle.Result, error) {
	if !wire.ValidSHA256(expected) {
		return bundle.Result{}, errors.New("invalid expected production release SHA-256")
	}
	result, err := inspect(ctx, policy, archive)
	if err != nil {
		return bundle.Result{}, err
	}
	if result.ArchiveSHA256 != expected {
		return bundle.Result{}, errors.New("the verified archive differs from the selected production release")
	}
	return result, nil
}

func TestProductionRestoreReleaseAdmission(t *testing.T) {
	digest := wire.Digest([]byte("selected release"))
	refused := errors.New("fixture signature refusal")
	for _, tc := range []struct {
		name, expected, actual string
		verifyErr              error
		verify, passes         bool
	}{
		{"missing pin", "", digest, nil, false, false},
		{"invalid pin", "sha256:" + digest, digest, nil, false, false},
		{"signature refusal", digest, digest, refused, true, false},
		{"different signed archive", digest, wire.Digest([]byte("other release")), nil, true, false},
		{"exact signed archive", digest, digest, nil, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			archive := "/release/ownsolid.tar.gz"
			policy := bundle.Verification{MountRoot: "/release", TrustFile: "/site/release-trust.json"}
			result, err := qualifyProductionRestoreMedia(t.Context(), archive, tc.expected, policy,
				func(_ context.Context, supplied bundle.Verification, filename string) (bundle.Result, error) {
					calls++
					if supplied != policy || filename != archive {
						t.Fatal("verification did not use the operator's staged archive and public trust")
					}
					return bundle.Result{ReleaseID: "candidate", ArchiveSHA256: tc.actual}, tc.verifyErr
				})
			if (err == nil) != tc.passes || (calls == 1) != tc.verify || !tc.passes && result.ReleaseID != "" {
				t.Fatalf("admission result %+v, error %v, verification calls %d", result, err, calls)
			}
			if tc.verifyErr != nil && !errors.Is(err, tc.verifyErr) {
				t.Fatalf("verifier error was lost: %v", err)
			}
		})
	}
}
