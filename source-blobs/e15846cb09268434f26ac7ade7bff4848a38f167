package models

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"syscall"

	"github.com/jackc/pgx/v5"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
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
	if e != nil || !info.Mode().IsRegular() || info.Size() != approved.SizeBytes || info.Mode().Perm() != 0444 {
		return out, fail("MODEL_FILE_INVALID")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Geteuid() || owner.Nlink != 1 {
		return out, fail("MODEL_FILE_INVALID")
	}
	file, e := root.Open(path)
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
	if e != nil || !os.SameFile(info, current) || info.Size() != current.Size() || !info.ModTime().Equal(current.ModTime()) {
		return out, fail("MODEL_FILE_CHANGED")
	}
	hash := sha256.New()
	count, e := io.Copy(hash, contextReader{ctx, io.NewSectionReader(file, input.Offset, input.Length)})
	if e != nil || count != input.Length {
		return out, fail("MODEL_IO_FAILED")
	}
	after, e := file.Stat()
	if e != nil || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return out, fail("MODEL_FILE_CHANGED")
	}
	keep = true
	return httpserver.Download{File: file, Offset: input.Offset, Size: input.Length, SHA256: hex.EncodeToString(hash.Sum(nil)), ContentType: "application/octet-stream"}, nil
}

// InvalidateAuthorities prevents restored background work from reusing an old administrator intent.
func InvalidateAuthorities(ctx context.Context, tx pgx.Tx) error {
	if tx == nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	return InvalidateAuthoritiesWithin(ctx, transaction.PostgreSQLTransaction(tx))
}
