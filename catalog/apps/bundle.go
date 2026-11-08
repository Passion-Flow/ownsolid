package apps

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
)

// BundleSigningDomain separates app inventory signatures from other Ed25519 uses.
const BundleSigningDomain = "ownsolid.app-bundle.v1\x00"

// ImageArchiveName is the fixed filename of the bundle's OCI image archive.
const ImageArchiveName = "images.tar"

// ErrBundle identifies an invalid signature, inventory or signed payload.
var ErrBundle = errors.New("CATALOG_BUNDLE_INVALID")

type bundleIndex struct {
	SchemaVersion int    `json:"schema_version"`
	Manifest      string `json:"manifest"`
	English       string `json:"en"`
	Chinese       string `json:"zh_cn"`
	Images        string `json:"images"`
}

// VerifyBundle authenticates the manifest and locales, then checks the bytes of
// the selected engine artifact and the image archive. The caller owns the
// immutable staging directory and supplies trusted publisher key material
// from outside the bundle. maximumPayloadBytes bounds the two payloads in sum.
// The caller must verify the OCI image identities and admit the rendered workload.
func VerifyBundle(ctx context.Context, root *os.Root, key ed25519.PublicKey, maximumPayloadBytes int64) (Release, error) {
	if root == nil || len(key) != ed25519.PublicKeySize || maximumPayloadBytes <= 0 {
		return Release{}, fmt.Errorf("%w: supply a local bundle, trusted publisher key and payload limit", ErrBundle)
	}
	if err := ctx.Err(); err != nil {
		return Release{}, err
	}
	index, err := readIndex(root, key)
	if err != nil {
		return Release{}, err
	}
	release, err := load(root, map[string]string{
		manifestName: index.Manifest, englishName: index.English, chineseName: index.Chinese,
	})
	if err != nil {
		return Release{}, fmt.Errorf("%w: %w", ErrBundle, err)
	}
	artifact := release.Manifest.Artifact
	bytes, err := verifyPayload(ctx, root, artifact.Filename(), artifact.SHA256, maximumPayloadBytes)
	if err != nil {
		return Release{}, err
	}
	if _, err = verifyPayload(ctx, root, ImageArchiveName, index.Images, maximumPayloadBytes-bytes); err != nil {
		return Release{}, err
	}
	return release, nil
}

func readIndex(root *os.Root, key ed25519.PublicKey) (bundleIndex, error) {
	raw, err := readLocalFile(root, "bundle.json", documentBytes)
	if err != nil {
		return bundleIndex{}, fmt.Errorf("%w: provide a bounded bundle.json", ErrBundle)
	}
	sig, err := readLocalFile(root, "bundle.sig", ed25519.SignatureSize)
	if err != nil || !ed25519.Verify(key, append([]byte(BundleSigningDomain), raw...), sig) {
		return bundleIndex{}, fmt.Errorf("%w: verify the publisher and replace the bundle or signature", ErrBundle)
	}
	var index bundleIndex
	if json.Unmarshal(raw, &index, json.RejectUnknownMembers(true)) != nil || index.SchemaVersion != 1 ||
		!digest.MatchString(index.Manifest) || !digest.MatchString(index.English) ||
		!digest.MatchString(index.Chinese) || !digest.MatchString(index.Images) {
		return bundleIndex{}, fmt.Errorf("%w: provide a version 1 index with SHA-256 digests", ErrBundle)
	}
	return index, nil
}

func verifyPayload(ctx context.Context, root *os.Root, name, expected string, maximum int64) (int64, error) {
	file, size, err := openLocalFile(root, name, maximum)
	if err != nil {
		return 0, fmt.Errorf("%w: provide a regular %s within the payload limit", ErrBundle, name)
	}
	defer file.Close()
	hash := sha256.New()
	reader := payloadReader{ctx: ctx, file: file}
	_, err = io.CopyN(hash, reader, size)
	if err == nil {
		var tail [1]byte
		_, tailErr := io.ReadFull(reader, tail[:])
		if tailErr != io.EOF {
			err = ErrBundle
		}
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return 0, ctxErr
	}
	if err != nil || encodeDigest(hash.Sum(nil)) != expected {
		return 0, fmt.Errorf("%w: replace %s with the exact signed payload", ErrBundle, name)
	}
	return size, nil
}

type payloadReader struct {
	ctx  context.Context
	file *os.File
}

func (r payloadReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.file.Read(buffer)
}

func digestBytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return encodeDigest(sum[:])
}

func encodeDigest(hash []byte) string { return "sha256:" + hex.EncodeToString(hash) }
