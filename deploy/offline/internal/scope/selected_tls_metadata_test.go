package scope

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Pure immutable-index validation receives no locked reader, filesystem path or
// Engine. Full ancestor admission remains in the controller's existing reader.
func TestSelectedTLSMetadataUsesValidatedPredecessorIndex(t *testing.T) {
	base, model := selectedTLSFixture(t)
	old := append([]engine.ConfigurationFile(nil), base.files["control-config"]...)
	slices.SortFunc(old, func(a, b engine.ConfigurationFile) int { return strings.Compare(a.Name, b.Name) })
	previous := RestoredTLSPlan{FormatVersion: 1, PayloadSHA256: restoreConfigurationDigest(old)}
	for _, f := range old {
		previous.ConfigurationFiles = append(previous.ConfigurationFiles, InputFile{Name: f.Name, Bytes: int64(len(f.Contents)), SHA256: wire.Digest(f.Contents)})
	}
	profile, e := selectedRestoredTLSProfile(base, model, "machine")
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	certificate, key, trust := selectedTLSCertificate(t, []string{"machine.example.test"}, now, 91)
	files, identity, certName, keyName, e := projectSelectedRestoredTLS(base, model, "machine", certificate, key, trust, now)
	if e != nil {
		t.Fatal(e)
	}
	p := RestoredTLSPlan{FormatVersion: 2, Request: configuration.TLSGenerationRequest{FormatVersion: 2, Target: "machine"}, CertificateName: certName, PrivateKeyName: keyName, Identity: identity, PreviousPayloadSHA256: previous.PayloadSHA256, PayloadSHA256: restoreConfigurationDigest(files)}
	for _, f := range files {
		p.ConfigurationFiles = append(p.ConfigurationFiles, InputFile{Name: f.Name, Bytes: int64(len(f.Contents)), SHA256: wire.Digest(f.Contents)})
	}
	if profile.CertificateName != p.CertificateName {
		t.Fatal("profile fixture drift")
	}
	for _, fault := range []string{"valid", "prior-payload", "prior-index", "inherited-byte-index", "missing-current", "reordered-current", "unchanged-certificate", "wrong-hosts", "selected-key-digest", "invalid-time"} {
		t.Run(fault, func(t *testing.T) {
			current, prior := p, previous
			current.ConfigurationFiles = slices.Clone(p.ConfigurationFiles)
			prior.ConfigurationFiles = slices.Clone(previous.ConfigurationFiles)
			switch fault {
			case "prior-payload":
				prior.PayloadSHA256 = strings.Repeat("f", 64)
			case "prior-index":
				prior.ConfigurationFiles[0].SHA256 = strings.Repeat("f", 64)
			case "inherited-byte-index":
				for i, f := range current.ConfigurationFiles {
					if f.Name != certName && f.Name != keyName {
						current.ConfigurationFiles[i].SHA256 = strings.Repeat("f", 64)
						break
					}
				}
				current.PayloadSHA256 = wire.Digest(encode(current.ConfigurationFiles))
			case "missing-current":
				current.ConfigurationFiles = current.ConfigurationFiles[1:]
				current.PayloadSHA256 = wire.Digest(encode(current.ConfigurationFiles))
			case "reordered-current":
				slices.Reverse(current.ConfigurationFiles)
				current.PayloadSHA256 = wire.Digest(encode(current.ConfigurationFiles))
			case "unchanged-certificate":
				for i, f := range current.ConfigurationFiles {
					if f.Name == certName {
						current.ConfigurationFiles[i] = prior.ConfigurationFiles[i]
						current.Identity.CertificateSHA256 = prior.ConfigurationFiles[i].SHA256
					}
				}
				current.PayloadSHA256 = wire.Digest(encode(current.ConfigurationFiles))
			case "wrong-hosts":
				current.Identity.Hosts = []string{"other.example.test"}
			case "selected-key-digest":
				current.Identity.PrivateKeySHA256 = strings.Repeat("f", 64)
			case "invalid-time":
				current.Identity.NotAfter = current.Identity.NotBefore
			}
			e := validateSelectedTLSMetadata(current, base, model, prior, 1<<20)
			if (e == nil) != (fault == "valid") {
				t.Fatal("immutable predecessor/current index decision", fault, e)
			}
		})
	}
}
