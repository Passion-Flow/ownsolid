package models

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"syscall"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/jackc/pgx/v5"
)

// ReadyInTransaction locks the scoped immutable version before creating a reference.
func (s *Service) ReadyInTransaction(ctx context.Context, tx pgx.Tx, tenant, workspace, id string) error {
	if tx == nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	return s.ReadyWithin(ctx, transaction.PostgreSQLTransaction(tx), tenant, workspace, id)
}

// OpenTransfer requires the owning caller's current authorized scope transaction.
// Machine entrypoints must separately validate current node/task/lease authority.
func (s *Service) OpenTransfer(ctx context.Context, tx pgx.Tx, tenant, workspace, model string, input wire.ModelFileRequest) (httpserver.Download, error) {
	if tx == nil {
		return httpserver.Download{}, fail("STORAGE_UNAVAILABLE")
	}
	return s.OpenTransferWithin(ctx, transaction.PostgreSQLTransaction(tx), tenant, workspace, model, input)
}
func (s *Service) OpenTransferWithin(ctx context.Context, q transaction.Executor, tenant, workspace, model string, input wire.ModelFileRequest) (httpserver.Download, error) {
	var out httpserver.Download
	if e := input.Validate(); e != nil {
		return out, e
	}
	if e := s.ReadyWithin(ctx, q, tenant, workspace, model); e != nil {
		return out, e
	}
	root, e := openStore(s.RootDirectory)
	if e != nil {
		return out, e
	}
	defer root.Close()
	for _, path := range []string{"objects", "objects/" + model} {
		info, e := root.Lstat(path)
		if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return out, fail("MODEL_FILE_INVALID")
		}
	}
	path := "objects/" + model + "/" + input.File
	info, e := root.Lstat(path)
	approved, _ := wire.ModelFileForName(input.File)
	if e != nil || !transferFileMatches(info, approved.SizeBytes) {
		return out, fail("MODEL_FILE_INVALID")
	}
	file, e := root.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return out, fail("MODEL_FILE_INVALID")
	}
	keep := false
	defer func() {
		if !keep {
			file.Close()
		}
	}()
	current, e := file.Stat()
	if e != nil || !transferFileMatches(current, approved.SizeBytes) || !os.SameFile(info, current) || !info.ModTime().Equal(current.ModTime()) {
		return out, fail("MODEL_FILE_CHANGED")
	}
	hash := sha256.New()
	count, e := io.Copy(hash, contextReader{ctx, io.NewSectionReader(file, input.Offset, input.Length)})
	if e != nil || count != input.Length {
		return out, fail("MODEL_IO_FAILED")
	}
	after, e := file.Stat()
	if e != nil || !transferFileMatches(after, approved.SizeBytes) || !after.ModTime().Equal(current.ModTime()) {
		return out, fail("MODEL_FILE_CHANGED")
	}
	digest, e := modelTransferDigest(input, approved, hash.Sum(nil))
	if e != nil {
		return out, e
	}
	keep = true
	return httpserver.Download{File: file, Offset: input.Offset, Size: input.Length, SHA256: digest, ContentType: "application/octet-stream"}, nil
}

// Check the opened descriptor as well as the pathname so the range stays
// tied to an ordinary, private immutable file throughout hashing.
func transferFileMatches(info os.FileInfo, size int64) bool {
	if !info.Mode().IsRegular() || info.Size() != size || info.Mode().Perm() != 0444 {
		return false
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	return ok && owner.Uid == uint32(os.Geteuid()) && owner.Nlink == 1
}

func modelTransferDigest(input wire.ModelFileRequest, asset wire.ModelFile, sum []byte) (string, error) {
	digest := hex.EncodeToString(sum)
	// A complete range can be checked against the pinned catalogue using
	// the existing response hash, without reopening or rereading the file.
	if input.Offset == 0 && input.Length == asset.SizeBytes && digest != asset.SHA256 {
		return "", fail("MODEL_FILE_CHANGED")
	}
	return digest, nil
}

// InvalidateAuthorities prevents restored background work from reusing an old administrator intent.
func InvalidateAuthorities(ctx context.Context, tx pgx.Tx) error {
	if tx == nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	return InvalidateAuthoritiesWithin(ctx, transaction.PostgreSQLTransaction(tx))
}
