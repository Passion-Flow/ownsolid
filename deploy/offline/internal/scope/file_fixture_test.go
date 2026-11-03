package scope

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Large actual image archives stay in files. Only the small synthetic metadata
// uses memory; this helper is never a production release signer.
func fixtureWithFileAssets(t *testing.T, extra []wire.Asset, sources []string) configuration.Config {
	return fixtureWithFileAssetsAt(t, extra, sources, "")
}

func fixtureWithFileAssetsAt(t *testing.T, extra []wire.Asset, sources []string, parent string) configuration.Config {
	t.Helper()
	if len(extra) != len(sources) {
		t.Fatal("fixture source count changed")
	}
	c := fixture(t)
	if parent != "" {
		if !filepath.IsAbs(parent) || filepath.Clean(parent) != parent || os.Mkdir(parent, 0700) != nil {
			t.Fatal("fresh private retained fixture parent required")
		}
		trust, e := os.ReadFile(c.Verification.TrustFile)
		if e != nil {
			t.Fatal(e)
		}
		c.Verification.TrustFile = filepath.Join(parent, "trust.json")
		put(t, c.Verification.TrustFile, trust)
		c.ScopeRoot = filepath.Join(parent, "scope")
	}
	small := [][]byte{[]byte("Synthetic test license"), []byte("Synthetic offline scope payload")}
	base, e := bundle.Inspect(context.Background(), c.Verification, c.BundleFile, c.ExpectedArchiveSHA256, c.ExpectedManifestSHA256)
	if e != nil {
		t.Fatal(e)
	}
	m := base.Manifest
	m.Assets = append(m.Assets, extra...)
	if e = m.Validate(c.Verification.MaximumAssets, c.Verification.MaximumAssetBytes, c.Verification.MaximumTotalBytes, true); e != nil {
		t.Fatal(e)
	}
	raw := encode(m)
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{43}, 32))
	public := key.Public().(ed25519.PublicKey)
	signature := encode(wire.Signature{Algorithm: wire.SignatureAlgorithm, KeyID: wire.Digest(public), ManifestSHA256: wire.Digest(raw), Value: base64.StdEncoding.EncodeToString(ed25519.Sign(key, append([]byte("projectone-offline-release-v1\n"), raw...)))})
	c.BundleFile = filepath.Join(filepath.Dir(c.ScopeRoot), "file-backed-fixture.tar.gz")
	file, e := os.OpenFile(c.BundleFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer file.Close()
	digest := sha256.New()
	gz, e := gzip.NewWriterLevel(io.MultiWriter(file, digest), gzip.BestSpeed)
	if e != nil {
		t.Fatal(e)
	}
	archive := tar.NewWriter(gz)
	writeSmall := func(name string, value []byte) {
		if e := archive.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(value)), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); e != nil {
			t.Fatal(e)
		}
		if _, e := archive.Write(value); e != nil {
			t.Fatal(e)
		}
	}
	writeSmall("manifest.json", raw)
	writeSmall("signature.json", signature)
	for i, a := range m.Assets[:len(small)] {
		writeSmall(a.Path, small[i])
	}
	for i, a := range extra {
		if e = writeFixtureFile(archive, a, sources[i]); e != nil {
			t.Fatal(e)
		}
	}
	for _, close := range []func() error{archive.Close, gz.Close, file.Sync, file.Close} {
		if e = close(); e != nil {
			t.Fatal(e)
		}
	}
	parentDirectory, e := os.Open(filepath.Dir(c.BundleFile))
	if e != nil {
		t.Fatal(e)
	}
	e = parentDirectory.Sync()
	parentDirectory.Close()
	if e != nil {
		t.Fatal(e)
	}
	c.ExpectedArchiveSHA256 = hex.EncodeToString(digest.Sum(nil))
	c.ExpectedManifestSHA256 = wire.Digest(raw)
	return c
}

func writeFixtureFile(archive *tar.Writer, asset wire.Asset, source string) error {
	if !wire.SafePath(asset.Path) || !wire.ValidSHA256(asset.SHA256) || asset.Size < 1 {
		return errors.New("invalid fixture asset")
	}
	fd, e := syscall.Open(source, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return e
	}
	file := os.NewFile(uintptr(fd), source)
	defer file.Close()
	before, e := file.Stat()
	if e != nil || !before.Mode().IsRegular() || before.Mode().Perm() != 0600 || before.Size() != asset.Size {
		return errors.New("private exact fixture file required")
	}
	owner, ok := before.Sys().(*syscall.Stat_t)
	if !ok || owner.Nlink != 1 || int(owner.Uid) != os.Geteuid() {
		return errors.New("owned single-link fixture file required")
	}
	if e = archive.WriteHeader(&tar.Header{Name: asset.Path, Mode: 0644, Size: asset.Size, Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); e != nil {
		return e
	}
	digest := sha256.New()
	n, e := io.CopyBuffer(io.MultiWriter(archive, digest), io.LimitReader(file, asset.Size+1), make([]byte, 32<<10))
	if e != nil {
		return e
	}
	after, e := file.Stat()
	named, nameError := os.Lstat(source)
	if e != nil || nameError != nil || !os.SameFile(before, after) || !os.SameFile(before, named) || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) || after.Mode() != before.Mode() || n != asset.Size || hex.EncodeToString(digest.Sum(nil)) != asset.SHA256 {
		return errors.New("fixture source changed during streaming")
	}
	return nil
}

func TestFileBackedSignedFixtureUsesActualFileAndRejectsChanges(t *testing.T) {
	raw := bytes.Repeat([]byte("bounded file-backed synthetic payload\n"), 65536)
	p := filepath.Join(t.TempDir(), "payload")
	put(t, p, raw)
	a := wire.Asset{ID: "file-backed", Path: "file-backed/data", Kind: "operator", Size: int64(len(raw)), SHA256: wire.Digest(raw), Licenses: []string{"license"}}
	c := fixtureWithFileAssets(t, []wire.Asset{a}, []string{p})
	result, e := bundle.Inspect(context.Background(), c.Verification, c.BundleFile, c.ExpectedArchiveSHA256, c.ExpectedManifestSHA256)
	if e != nil || result.Assets != 3 || !bytes.Equal(encode(result.Manifest.Assets[2]), encode(a)) {
		t.Fatal("streamed signed fixture verification failed", e)
	}
	for _, fault := range []string{"digest", "size", "symlink", "mode", "hardlink"} {
		t.Run(fault, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "payload")
			put(t, path, raw)
			value := a
			switch fault {
			case "digest":
				value.SHA256 = wire.Digest([]byte("changed"))
			case "size":
				value.Size++
			case "symlink":
				link := path + ".link"
				if os.Symlink(path, link) != nil {
					t.Fatal("link fixture")
				}
				path = link
			case "mode":
				if os.Chmod(path, 0644) != nil {
					t.Fatal("mode fixture")
				}
			case "hardlink":
				if os.Link(path, path+".link") != nil {
					t.Fatal("hardlink fixture")
				}
			}
			archive := tar.NewWriter(io.Discard)
			if writeFixtureFile(archive, value, path) == nil {
				t.Fatal("changed fixture source accepted")
			}
		})
	}
}
