package factory

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const (
	rootTrustFormatVersion = 2
	rootTrustDomain        = "ownsolid-offline-release-root-v2\n"
	maxRootMetadataBytes   = 65536
)

type rootTrust struct {
	FormatVersion int               `json:"format_version"`
	Version       uint64            `json:"version"`
	Expires       time.Time         `json:"expires"`
	Threshold     int               `json:"root_threshold"`
	RootKeys      map[string]string `json:"root_keys"`
	ReleaseKeys   map[string]string `json:"release_keys"`
}

type parsedRoot struct {
	rootTrust
	root    map[string]ed25519.PublicKey
	release map[string]ed25519.PublicKey
}

// RootSignature is one detached Ed25519 signature for a successor root.
type RootSignature struct {
	KeyID string `json:"key_id"`
	Value string `json:"signature"`
}

func parseRootTrust(raw []byte) (parsedRoot, error) {
	var out parsedRoot
	if len(raw) > maxRootMetadataBytes || wire.StrictJSON(raw, &out.rootTrust) != nil ||
		out.FormatVersion != rootTrustFormatVersion || out.Version == 0 || out.Expires.IsZero() ||
		out.Threshold < 1 || out.Threshold > len(out.RootKeys) {
		return parsedRoot{}, fail("RELEASE_TRUST_ROOT_INVALID")
	}
	var err error
	out.root, err = decodePublicKeys(out.RootKeys)
	if err != nil {
		return parsedRoot{}, fail("RELEASE_TRUST_ROOT_INVALID")
	}
	out.release, err = decodePublicKeys(out.ReleaseKeys)
	if err != nil {
		return parsedRoot{}, fail("RELEASE_TRUST_ROOT_INVALID")
	}
	for id := range out.root {
		if _, duplicate := out.release[id]; duplicate {
			return parsedRoot{}, fail("RELEASE_TRUST_ROOT_ROLES_OVERLAP")
		}
	}
	return out, nil
}

func isVersionedTrust(raw []byte) bool {
	var fields map[string]json.RawMessage
	return json.Unmarshal(raw, &fields) == nil && fields["format_version"] != nil
}

func releaseTrustKeys(raw []byte, now time.Time) (map[string]ed25519.PublicKey, error) {
	root, err := parseRootTrust(raw)
	if err != nil {
		return nil, err
	}
	if !root.Expires.After(now) {
		return nil, fail("RELEASE_TRUST_ROOT_EXPIRED")
	}
	return root.release, nil
}

// SignRoot writes one detached signature on an offline signing host.
func SignRoot(candidateFile, keyFile string) (RootSignature, error) {
	raw, err := readRegular(candidateFile, maxRootMetadataBytes, true)
	if err != nil {
		return RootSignature{}, err
	}
	if _, err := parseRootTrust(raw); err != nil {
		return RootSignature{}, err
	}
	key, err := readReleaseSigningKey(keyFile)
	if err != nil {
		return RootSignature{}, err
	}
	public := key.Public().(ed25519.PublicKey)
	return RootSignature{
		KeyID: wire.Digest(public),
		Value: base64.StdEncoding.EncodeToString(ed25519.Sign(key, append([]byte(rootTrustDomain), raw...))),
	}, nil
}

func verifyRootTransition(current, candidate []byte, signatures []RootSignature) (uint64, error) {
	old, err := parseRootTrust(current)
	if err != nil {
		return 0, err
	}
	next, err := parseRootTrust(candidate)
	if err != nil {
		return 0, err
	}
	if next.Version-1 != old.Version {
		return 0, fail("RELEASE_TRUST_ROOT_VERSION_NOT_SUCCESSOR")
	}
	if len(signatures) < 1 || len(signatures) > 32 {
		return 0, fail("RELEASE_TRUST_ROOT_SIGNATURE_INVALID")
	}
	oldCount, nextCount := 0, 0
	message := append([]byte(rootTrustDomain), candidate...)
	seen := make(map[string]bool, len(signatures))
	for _, signature := range signatures {
		if seen[signature.KeyID] {
			return 0, fail("RELEASE_TRUST_ROOT_DUPLICATE_SIGNATURE")
		}
		seen[signature.KeyID] = true
		key := old.root[signature.KeyID]
		if key == nil {
			key = next.root[signature.KeyID]
		}
		if key == nil {
			return 0, fail("RELEASE_TRUST_ROOT_UNKNOWN_SIGNER")
		}
		sig, err := base64.StdEncoding.Strict().DecodeString(signature.Value)
		if err != nil || base64.StdEncoding.EncodeToString(sig) != signature.Value || !ed25519.Verify(key, message, sig) {
			return 0, fail("RELEASE_TRUST_ROOT_SIGNATURE_INVALID")
		}
		if old.root[signature.KeyID] != nil {
			oldCount++
		}
		if next.root[signature.KeyID] != nil {
			nextCount++
		}
	}
	if oldCount < old.Threshold || nextCount < next.Threshold {
		return 0, fail("RELEASE_TRUST_ROOT_THRESHOLD_NOT_MET")
	}
	return next.Version, nil
}

// RootUpdate names one successor and its protected detached signature list.
type RootUpdate struct {
	Candidate, Signatures string
}

// RotateRoot replaces a pinned public trust file only after verifying each
// immediate successor in the supplied chain and the final root's expiry.
func RotateRoot(trustFile string, updates []RootUpdate, now time.Time) (uint64, error) {
	if len(updates) < 1 || len(updates) > 32 {
		return 0, fail("RELEASE_TRUST_ROOT_CHAIN_INVALID")
	}
	directory := filepath.Dir(trustFile)
	if privateDirectory(directory) != nil {
		return 0, fail("RELEASE_TRUST_ROOT_PRIVATE_DIRECTORY_REQUIRED")
	}
	lock, err := os.OpenFile(trustFile+".lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return 0, fail("RELEASE_TRUST_ROOT_LOCK_UNAVAILABLE")
	}
	defer lock.Close()
	info, err := lock.Stat()
	if err != nil || !info.Mode().IsRegular() || !owned(info) || info.Mode().Perm()&0o077 != 0 ||
		syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return 0, fail("RELEASE_TRUST_ROOT_LOCK_UNAVAILABLE")
	}
	current, err := readRegular(trustFile, maxRootMetadataBytes, true)
	if err != nil {
		return 0, err
	}
	var version uint64
	for _, update := range updates {
		candidate, err := readRegular(update.Candidate, maxRootMetadataBytes, true)
		if err != nil {
			return 0, err
		}
		raw, err := readRegular(update.Signatures, maxRootMetadataBytes, true)
		var signatures []RootSignature
		if err != nil || wire.StrictJSON(raw, &signatures) != nil {
			return 0, fail("RELEASE_TRUST_ROOT_SIGNATURE_INVALID")
		}
		version, err = verifyRootTransition(current, candidate, signatures)
		if err != nil {
			return 0, err
		}
		current = candidate
	}
	if _, err := releaseTrustKeys(current, now); err != nil {
		return 0, err
	}
	file, err := os.CreateTemp(directory, ".ownsolid-root-")
	if err != nil {
		return 0, fail("RELEASE_TRUST_ROOT_WRITE_FAILED")
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(current)
	if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
		return 0, fail("RELEASE_TRUST_ROOT_WRITE_FAILED")
	}
	if err := os.Rename(file.Name(), trustFile); err != nil {
		return 0, fail("RELEASE_TRUST_ROOT_WRITE_FAILED")
	}
	parent, err := os.Open(directory)
	if err != nil {
		return 0, fail("RELEASE_TRUST_ROOT_SYNC_UNCERTAIN")
	}
	if err := errors.Join(parent.Sync(), parent.Close()); err != nil {
		return 0, fail("RELEASE_TRUST_ROOT_SYNC_UNCERTAIN")
	}
	return version, nil
}
