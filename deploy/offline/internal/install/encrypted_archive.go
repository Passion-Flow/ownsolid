package install

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"filippo.io/age"
)

const (
	encryptedManifestLimit = 64 << 20
	tarBlockSize           = 512
)

// The existing manifest determines the only allowed paths and their order in
// the tar stream. Files outside the roots copied by backup are never extracted.
func encryptedMembers(manifest backupManifest) ([]backupFile, error) {
	db := manifest.Database
	if db.Path != backupDatabaseName || db.Bytes < 1 {
		return nil, errArchiveInvalid
	}
	members := []backupFile{db}
	seen := make(map[string]bool, len(manifest.Files))
	for _, file := range manifest.Files {
		if !fs.ValidPath(file.Path) || file.Bytes < 0 || seen[file.Path] ||
			!strings.HasPrefix(file.Path, "config/") && !strings.HasPrefix(file.Path, "operator/") &&
				!strings.HasPrefix(file.Path, "secrets/") && !strings.HasPrefix(file.Path, "data/models/objects/") {
			return nil, errArchiveInvalid
		}
		seen[file.Path] = true
		file.Path = path.Join(backupFilesName, file.Path)
		members = append(members, file)
	}
	return members, nil
}

func writeEncryptedArchive(ctx context.Context, output io.Writer, source string, recipient *age.X25519Recipient) error {
	manifestPath := filepath.Join(source, backupManifestName)
	f, err := os.Open(manifestPath)
	if err != nil {
		return err
	}
	raw, err := io.ReadAll(io.LimitReader(f, encryptedManifestLimit+1))
	f.Close()
	if err != nil {
		return err
	}
	if len(raw) > encryptedManifestLimit {
		return errors.New("the backup manifest exceeds the encrypted archive format limit")
	}
	manifest, err := decodeBackupManifest(raw)
	if err != nil {
		return err
	}
	members, err := encryptedMembers(manifest)
	if err != nil {
		return err
	}
	encryption, err := age.Encrypt(output, recipient)
	if err != nil {
		return err
	}
	archive := tar.NewWriter(encryption)
	if err := archive.WriteHeader(archiveHeader(backupManifestName, int64(len(raw)))); err != nil {
		return err
	}
	if _, err := archive.Write(raw); err != nil {
		return err
	}
	for _, member := range members {
		if err := writeArchiveMember(ctx, archive, source, member); err != nil {
			return err
		}
	}
	if err := archive.Close(); err != nil {
		return err
	}
	return encryption.Close()
}

func archiveHeader(name string, size int64) *tar.Header {
	return &tar.Header{Name: name, Size: size, Mode: 0o600, Typeflag: tar.TypeReg, Format: tar.FormatPAX}
}

func writeArchiveMember(ctx context.Context, archive *tar.Writer, source string, member backupFile) error {
	if err := archive.WriteHeader(archiveHeader(member.Path, member.Bytes)); err != nil {
		return err
	}
	f, err := os.Open(filepath.Join(source, filepath.FromSlash(member.Path)))
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(archive, archiveReader{ctx, f})
	return err
}

func readEncryptedArchive(ctx context.Context, plaintext io.Reader, target string) (backupManifest, error) {
	input := &archiveCounter{Reader: archiveReader{ctx, plaintext}}
	archive := tar.NewReader(input)
	first, err := archive.Next()
	if err != nil || first.Name != backupManifestName || first.Typeflag != tar.TypeReg ||
		first.Size < 1 || first.Size > encryptedManifestLimit {
		return backupManifest{}, errArchiveInvalid
	}
	raw, err := io.ReadAll(archive)
	if err != nil {
		return backupManifest{}, errArchiveInvalid
	}
	manifest, err := decodeBackupManifest(raw)
	if err != nil || manifest.Recovery != nil {
		return backupManifest{}, errArchiveInvalid
	}
	members, err := encryptedMembers(manifest)
	if err != nil {
		return backupManifest{}, err
	}
	for _, member := range members {
		header, err := archive.Next()
		if err != nil || header.Typeflag != tar.TypeReg || header.Name != member.Path || header.Size != member.Bytes {
			return backupManifest{}, errArchiveInvalid
		}
		if err := extractArchiveMember(archive, target, member); err != nil {
			return backupManifest{}, err
		}
	}
	// archive/tar also accepts EOF without both terminating blocks. The writer
	// always emits them, so distinguish that incomplete framing from tar EOF.
	read := input.bytes
	lastSize := members[len(members)-1].Bytes
	padding := (tarBlockSize - lastSize%tarBlockSize) % tarBlockSize
	if _, err := archive.Next(); err != io.EOF || input.bytes-read != padding+2*tarBlockSize {
		return backupManifest{}, errArchiveInvalid
	}
	// Tar's end marker can precede age's final chunk. Require a real end of
	// stream even after tar EOF so truncation and trailing data both fail.
	var trailing [1]byte
	if _, err := io.ReadFull(input, trailing[:]); err != io.EOF {
		return backupManifest{}, errArchiveInvalid
	}
	return manifest, nil
}

func extractArchiveMember(source io.Reader, target string, member backupFile) error {
	path := filepath.Join(target, filepath.FromSlash(member.Path))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	digest := sha256.New()
	written, err := io.Copy(io.MultiWriter(f, digest), source)
	closeErr := f.Close()
	if err != nil || written != member.Bytes || hex.EncodeToString(digest.Sum(nil)) != member.SHA256 {
		return errArchiveInvalid
	}
	return closeErr
}

type archiveReader struct {
	ctx context.Context
	io.Reader
}

func (r archiveReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

type archiveCounter struct {
	io.Reader
	bytes int64
}

func (r *archiveCounter) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.bytes += int64(n)
	return n, err
}
