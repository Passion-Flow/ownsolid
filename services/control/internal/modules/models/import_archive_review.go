package models

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"

	"github.com/Passion-Flow/ownsolid/api/agent/modelartifact"
)

const (
	importArchiveBlockBytes = 512
	// The USTAR type flag follows the checksum in the fixed header.
	importArchiveTypeOffset = 156
	// Match the shared extractor's two terminal blocks and zero-padding limit.
	importArchiveTerminalBytes = 2 * importArchiveBlockBytes
	importArchiveMaxTrailer    = importArchiveTerminalBytes + 20*importArchiveBlockBytes
)

func readImportArchiveAt(source io.ReaderAt, p []byte, offset int64) error {
	n, err := source.ReadAt(p, offset)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return err
	}
	if n != len(p) {
		return fail("MODEL_ARCHIVE_INVALID")
	}
	return nil
}

// reviewArchive examines the complete member layout before extraction. The
// caller still has to stream and authenticate every chunk and file to publish.
func (m *importManifest) reviewArchive(source io.ReaderAt, size int64) error {
	if m.catalog.Format != "safetensors" || !m.admitsArchiveSize(size) {
		return fail("MODEL_ARCHIVE_INVALID")
	}
	var block [importArchiveBlockBytes]byte
	var offset int64
	seen := make(map[string]bool, len(m.catalog.Files))
	for range m.catalog.Files {
		if err := readImportArchiveAt(source, block[:], offset); err != nil {
			return err
		}
		// Prevent tar.Reader from consuming extension records while looking
		// for the next regular header. Only this one block reaches the parser.
		if block[importArchiveTypeOffset] != tar.TypeReg && block[importArchiveTypeOffset] != tar.TypeRegA {
			return fail("MODEL_ARCHIVE_INVALID")
		}
		header, err := tar.NewReader(bytes.NewReader(block[:])).Next()
		if err != nil || header.Format != tar.FormatUSTAR || header.Typeflag != tar.TypeReg ||
			header.Linkname != "" || header.Mode&0111 != 0 {
			return fail("MODEL_ARCHIVE_INVALID")
		}
		file, ok := m.file(header.Name)
		payload := offset + importArchiveBlockBytes
		if !ok || seen[header.Name] || header.Size != file.SizeBytes || header.Size < 0 || header.Size > size-payload {
			return fail("MODEL_ARCHIVE_INVALID")
		}
		seen[header.Name] = true
		content := io.NewSectionReader(source, payload, header.Size)
		switch header.Name {
		case "model.safetensors":
			if err = modelartifact.VerifySafetensors(content, header.Size); err != nil {
				return err
			}
		case "config.json", "tokenizer_config.json":
			hash := sha256.New()
			n, err := io.Copy(hash, content)
			if err != nil {
				return err
			}
			if n != header.Size || hex.EncodeToString(hash.Sum(nil)) != file.SHA256 {
				return fail("MODEL_DIGEST_MISMATCH")
			}
		}
		offset = payload + (header.Size+importArchiveBlockBytes-1)/importArchiveBlockBytes*importArchiveBlockBytes
	}

	trailerBytes := size - offset
	if trailerBytes < importArchiveTerminalBytes || trailerBytes > importArchiveMaxTrailer {
		return fail("MODEL_ARCHIVE_INVALID")
	}
	var trailer [importArchiveMaxTrailer]byte
	if err := readImportArchiveAt(source, trailer[:trailerBytes], offset); err != nil {
		return err
	}
	for _, b := range trailer[:trailerBytes] {
		if b != 0 {
			return fail("MODEL_ARCHIVE_INVALID")
		}
	}
	return nil
}
