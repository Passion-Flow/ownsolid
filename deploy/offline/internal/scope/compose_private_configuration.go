package scope

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"syscall"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/composeprofile"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const composePrivateJournal = ".projectone-private-install"
const composePrivateStage = composePrivateJournal + "/stage"
const composePrivateIntent = composePrivateJournal + "/intent.json"
const composePrivateReady = composePrivateJournal + "/ready.json"
const composePrivateCommitted = composePrivateJournal + "/committed.json"

type ComposePrivateResult struct {
	Status        string `json:"status"`
	ScopeID       string `json:"scope_id"`
	OperationID   string `json:"operation_id"`
	VolumeName    string `json:"volume_name"`
	RequestSHA256 string `json:"request_sha256"`
	Files         int    `json:"files"`
	Bytes         int64  `json:"bytes"`
	Qualification string `json:"qualification"`
}

func privateConfigurationError(code string) error {
	return failure("OFFLINE_SCOPE_PRIVATE_CONFIGURATION_" + code)
}

func privateMetadata(info os.FileInfo, mode os.FileMode, uid, gid int) bool {
	if info == nil || info.Mode() != mode {
		return false
	}
	s, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(s.Uid) == uid && int(s.Gid) == gid && (mode.IsDir() || s.Nlink == 1)
}

func pinConfigurationDirectory(name string, mode os.FileMode, uid, gid int) (*os.Root, os.FileInfo, error) {
	before, e := os.Lstat(name)
	if e != nil || !canonical(name) || !privateMetadata(before, mode, uid, gid) {
		return nil, nil, privateConfigurationError("DIRECTORY_REFUSED")
	}
	root, e := os.OpenRoot(name + string(filepath.Separator) + ".")
	if e != nil {
		return nil, nil, privateConfigurationError("DIRECTORY_REFUSED")
	}
	if !configurationDirectoryUnchanged(root, before, mode, uid, gid) {
		root.Close()
		return nil, nil, privateConfigurationError("DIRECTORY_CHANGED")
	}
	return root, before, nil
}

func configurationDirectoryUnchanged(root *os.Root, before os.FileInfo, mode os.FileMode, uid, gid int) bool {
	name := filepath.Clean(root.Name())
	named, ne := os.Lstat(name)
	opened, oe := root.Stat(".")
	return ne == nil && oe == nil && canonical(name) && privateMetadata(named, mode, uid, gid) && privateMetadata(opened, mode, uid, gid) && os.SameFile(before, named) && os.SameFile(before, opened)
}

func privateRead(root *os.Root, name string, uid, gid int, maximum int64) (result []byte, err error) {
	bad := privateConfigurationError("FILE_REFUSED")
	before, e := root.Lstat(name)
	if e != nil || !privateMetadata(before, 0600, uid, gid) || before.Size() < 1 || before.Size() > maximum {
		return nil, bad
	}
	f, e := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, bad
	}
	defer func() {
		if f.Close() != nil {
			result, err = nil, bad
		}
	}()
	opened, e := f.Stat()
	if e != nil || !privateMetadata(opened, 0600, uid, gid) || !os.SameFile(before, opened) {
		return nil, bad
	}
	raw, e := io.ReadAll(io.LimitReader(f, maximum+1))
	after, ae := f.Stat()
	named, ne := root.Lstat(name)
	for _, v := range []os.FileInfo{opened, after, named} {
		if v == nil || !privateMetadata(v, 0600, uid, gid) || !os.SameFile(before, v) || before.Size() != v.Size() || before.ModTime() != v.ModTime() || !samePrivateChangeTime(before, v) {
			return nil, bad
		}
	}
	if e != nil || ae != nil || ne != nil || int64(len(raw)) != before.Size() {
		return nil, bad
	}
	return raw, nil
}

func privateEntries(root *os.Root, name string) ([]string, error) {
	f, e := root.Open(name + "/.")
	if e != nil {
		return nil, privateConfigurationError("INVENTORY_REFUSED")
	}
	defer f.Close()
	entries, e := f.ReadDir(258)
	if e != nil && e != io.EOF {
		return nil, privateConfigurationError("INVENTORY_REFUSED")
	}
	if len(entries) > 257 {
		return nil, privateConfigurationError("INVENTORY_REFUSED")
	}
	result := make([]string, 0, len(entries))
	for _, v := range entries {
		result = append(result, v.Name())
	}
	slices.Sort(result)
	return result, nil
}

// The explicit durability boundary permits deterministic filesystem failures in
// tests without global hooks. Production always supplies native fsync calls.
type privateDurability interface {
	File(*os.File) error
	Directory(*os.File) error
}
type nativePrivateDurability struct{}

func (nativePrivateDurability) File(f *os.File) error      { return f.Sync() }
func (nativePrivateDurability) Directory(f *os.File) error { return f.Sync() }

func privateSync(root *os.Root, name string, durability privateDurability) error {
	f, e := root.Open(name + "/.")
	if e != nil {
		return privateConfigurationError("DURABILITY_UNCERTAIN")
	}
	e = durability.Directory(f)
	ce := f.Close()
	if e != nil || ce != nil {
		return privateConfigurationError("DURABILITY_UNCERTAIN")
	}
	return nil
}

func privateSyncFile(root *os.Root, name string, uid, gid int, durability privateDurability) error {
	bad := privateConfigurationError("DURABILITY_UNCERTAIN")
	before, e := root.Lstat(name)
	if e != nil || !privateMetadata(before, 0600, uid, gid) {
		return bad
	}
	// This helper is used only by explicit mutation/recovery, never inspection.
	// Open for writing as required by stricter fsync implementations, without
	// truncating, creating, rewriting or changing any file metadata.
	f, e := root.OpenFile(name, os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return bad
	}
	opened, se := f.Stat()
	if se != nil || !os.SameFile(before, opened) || !privateMetadata(opened, 0600, uid, gid) {
		f.Close()
		return bad
	}
	e = durability.File(f)
	after, ae := f.Stat()
	named, ne := root.Lstat(name)
	ce := f.Close()
	if e != nil || ae != nil || ne != nil || ce != nil || !os.SameFile(before, after) || !os.SameFile(before, named) || !samePrivateChangeTime(before, after) || !samePrivateChangeTime(before, named) {
		return bad
	}
	return nil
}

func privateWrite(root *os.Root, name string, raw []byte, uid, gid int, durability privateDurability) error {
	f, e := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return privateConfigurationError("WRITE_UNCERTAIN")
	}
	n, we := f.Write(raw)
	ce := f.Chown(uid, gid)
	se := durability.File(f)
	closeError := f.Close()
	if we != nil || n != len(raw) || ce != nil || se != nil || closeError != nil {
		return privateConfigurationError("WRITE_UNCERTAIN")
	}
	return nil
}

func privatePayload(ctx context.Context, root *os.Root, p composeprofile.PrivateConfiguration, uid, gid int) ([]engine.ConfigurationFile, error) {
	entries, e := privateEntries(root, ".")
	if e != nil || len(entries) != len(p.Files) {
		return nil, privateConfigurationError("INVENTORY_REFUSED")
	}
	files := make([]engine.ConfigurationFile, 0, len(entries))
	for i, v := range p.Files {
		if ctx.Err() != nil {
			return nil, privateConfigurationError("INTERRUPTED")
		}
		if entries[i] != v.Name {
			return nil, privateConfigurationError("INVENTORY_REFUSED")
		}
		raw, e := privateRead(root, v.Name, uid, gid, p.MaximumFileBytes)
		if e != nil || int64(len(raw)) != v.Bytes || wire.Digest(raw) != v.SHA256 {
			return nil, privateConfigurationError("PAYLOAD_REFUSED")
		}
		files = append(files, engine.ConfigurationFile{Name: v.Name, Contents: raw})
	}
	return files, nil
}

// This ordinary Compose filesystem transaction is deliberately separate from
// the signed M17 scope journal. It cannot admit images, mutate a live scope or
// authorize Engine effects. The caller must bind the exact unused named volume.
// Publication keeps M17's private-child layout and intent/observe/recover rule.
func RunComposePrivateConfiguration(ctx context.Context, p composeprofile.PrivateConfiguration, action string) (ComposePrivateResult, error) {
	if p.Validate() != nil || os.Geteuid() != 0 {
		return ComposePrivateResult{}, privateConfigurationError("PRIVILEGED_FIXED_INSTALLER_REQUIRED")
	}
	if e := engine.VerifyNativeConfigurationMounts(p.SourceRoot, p.TargetRoot, action); e != nil {
		return ComposePrivateResult{}, e
	}
	return runComposePrivateConfiguration(ctx, p, action)
}

func runComposePrivateConfiguration(ctx context.Context, p composeprofile.PrivateConfiguration, action string) (ComposePrivateResult, error) {
	return runComposePrivateConfigurationWithDurability(ctx, p, action, nativePrivateDurability{})
}

func runComposePrivateConfigurationWithDurability(ctx context.Context, p composeprofile.PrivateConfiguration, action string, durability privateDurability) (ComposePrivateResult, error) {
	result := ComposePrivateResult{ScopeID: p.ScopeID, OperationID: p.OperationID, VolumeName: p.VolumeName, RequestSHA256: p.Fingerprint(), Qualification: "native_private_configuration_only_not_signed_scope_install_or_readiness"}
	bad := func(e error) (ComposePrivateResult, error) { return ComposePrivateResult{}, e }
	if p.Validate() != nil || (action != "install" && action != "inspect" && action != "recover") {
		return bad(privateConfigurationError("REQUEST_REFUSED"))
	}
	if ctx.Err() != nil {
		return bad(privateConfigurationError("INTERRUPTED"))
	}
	owner, group := os.Geteuid(), os.Getegid()
	root, before, e := pinConfigurationDirectory(p.TargetRoot, os.ModeDir|0755, owner, group)
	if e != nil {
		return bad(e)
	}
	defer root.Close()
	unchanged := func() bool { return configurationDirectoryUnchanged(root, before, os.ModeDir|0755, owner, group) }
	entries, e := privateEntries(root, ".")
	if e != nil {
		return bad(e)
	}
	if len(entries) == 0 && action == "inspect" {
		result.Status = "empty_uninitialized"
		return result, nil
	}
	if action == "install" {
		if len(entries) != 0 {
			return bad(privateConfigurationError("EXISTING_STATE_PRESERVED"))
		}
		var filesystem syscall.Statfs_t
		peak := p.MaximumTotalBytes + 2*composeprofile.PrivateConfigurationMaximumRequestBytes + int64(len(p.Files)+16)*8192
		if syscall.Statfs(p.TargetRoot, &filesystem) != nil || float64(filesystem.Bavail)*float64(filesystem.Bsize)-float64(peak) < float64(filesystem.Blocks)*float64(filesystem.Bsize)*0.30 {
			return bad(privateConfigurationError("STORAGE_HEADROOM_REQUIRED"))
		}
		// Verify and freeze every byte before creating the durable intent.
		source, sourceBefore, e := pinConfigurationDirectory(p.SourceRoot, os.ModeDir|0700, p.SourceUID, p.SourceGID)
		if e != nil {
			return bad(e)
		}
		defer source.Close()
		files, e := privatePayload(ctx, source, p, p.SourceUID, p.SourceGID)
		if e != nil {
			return bad(e)
		}
		if !configurationDirectoryUnchanged(source, sourceBefore, os.ModeDir|0700, p.SourceUID, p.SourceGID) || !unchanged() {
			return bad(privateConfigurationError("DIRECTORY_CHANGED"))
		}
		if root.Mkdir(composePrivateJournal, 0700) != nil {
			return bad(privateConfigurationError("INTENT_UNCERTAIN"))
		}
		lock, e := root.OpenFile(composePrivateJournal+"/lock", os.O_CREATE|os.O_EXCL|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
		if e != nil {
			return bad(privateConfigurationError("INTENT_UNCERTAIN"))
		}
		defer lock.Close()
		if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
			return bad(privateConfigurationError("LOCKED"))
		}
		raw, _ := json.Marshal(p)
		if e = privateWrite(root, composePrivateIntent, append(raw, '\n'), owner, group, durability); e != nil {
			return bad(e)
		}
		if lock.Sync() != nil {
			return bad(privateConfigurationError("DURABILITY_UNCERTAIN"))
		}
		for _, dir := range []string{composePrivateJournal, "."} {
			if e = privateSync(root, dir, durability); e != nil {
				return bad(e)
			}
		}
		if ctx.Err() != nil {
			return bad(privateConfigurationError("INTERRUPTED"))
		}
		if root.Mkdir(composePrivateStage, 0700) != nil {
			return bad(privateConfigurationError("STAGE_UNCERTAIN"))
		}
		for _, file := range files {
			if ctx.Err() != nil {
				return bad(privateConfigurationError("INTERRUPTED"))
			}
			if e = privateWrite(root, composePrivateStage+"/"+file.Name, file.Contents, p.UID, p.GID, durability); e != nil {
				return bad(e)
			}
		}
		if e = root.Chown(composePrivateStage, p.UID, p.GID); e != nil {
			return bad(privateConfigurationError("STAGE_UNCERTAIN"))
		}
		if e = privateSync(root, composePrivateStage, durability); e != nil {
			return bad(e)
		}
		if e = privateSync(root, composePrivateJournal, durability); e != nil {
			return bad(e)
		}
		if !unchanged() {
			return bad(privateConfigurationError("DIRECTORY_CHANGED"))
		}
		if e = verifyPrivateGeneration(ctx, p, composePrivateStage); e != nil {
			return bad(e)
		}
		if e = privateWrite(root, composePrivateReady, []byte(p.Fingerprint()+"\n"), owner, group, durability); e != nil {
			return bad(e)
		}
		if e = privateSync(root, composePrivateJournal, durability); e != nil {
			return bad(e)
		}
		return publishPrivateGeneration(ctx, root, before, p, result, durability)
	}
	if !slices.Equal(entries, []string{composePrivateJournal}) && !slices.Equal(entries, []string{composePrivateJournal, engine.UtilityPrivateDirectory}) {
		return bad(privateConfigurationError("FOREIGN_STATE_PRESERVED"))
	}
	journal, journalBefore, e := pinConfigurationDirectory(filepath.Join(p.TargetRoot, composePrivateJournal), os.ModeDir|0700, owner, group)
	if e != nil {
		return bad(e)
	}
	defer journal.Close()
	lock, e := journal.OpenFile("lock", os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return bad(privateConfigurationError("JOURNAL_REFUSED"))
	}
	defer lock.Close()
	li, le := lock.Stat()
	ln, lne := journal.Lstat("lock")
	if le != nil || lne != nil || !privateMetadata(li, 0600, owner, group) || !privateMetadata(ln, 0600, owner, group) || li.Size() != 0 || !os.SameFile(li, ln) {
		return bad(privateConfigurationError("JOURNAL_REFUSED"))
	}
	flock := syscall.LOCK_EX | syscall.LOCK_NB
	if action == "inspect" {
		flock = syscall.LOCK_SH | syscall.LOCK_NB
	}
	if syscall.Flock(int(lock.Fd()), flock) != nil {
		return bad(privateConfigurationError("LOCKED"))
	}
	raw, e := privateRead(journal, "intent.json", owner, group, composeprofile.PrivateConfigurationMaximumRequestBytes)
	var stored composeprofile.PrivateConfiguration
	if e != nil || wire.StrictJSON(raw, &stored) != nil || stored.Validate() != nil || stored.Fingerprint() != p.Fingerprint() {
		return bad(privateConfigurationError("JOURNAL_CONTEXT_MISMATCH"))
	}
	list, e := privateEntries(journal, ".")
	if e != nil {
		return bad(e)
	}
	allowed := map[string]bool{"lock": true, "intent.json": true, "stage": true, "ready.json": true, "committed.json": true}
	for _, name := range list {
		if !allowed[name] {
			return bad(privateConfigurationError("FOREIGN_STATE_PRESERVED"))
		}
	}
	ready := slices.Contains(list, "ready.json")
	committed := slices.Contains(list, "committed.json")
	stage := slices.Contains(list, "stage")
	published := slices.Contains(entries, engine.UtilityPrivateDirectory)
	if published && stage || committed && !published || published && !ready {
		return bad(privateConfigurationError("JOURNAL_REFUSED"))
	}
	for _, name := range []string{"ready.json", "committed.json"} {
		if !slices.Contains(list, name) {
			continue
		}
		raw, e = privateRead(journal, name, owner, group, 128)
		if e != nil || !bytes.Equal(raw, []byte(p.Fingerprint()+"\n")) {
			return bad(privateConfigurationError("JOURNAL_REFUSED"))
		}
	}
	if !unchanged() || !configurationDirectoryUnchanged(journal, journalBefore, os.ModeDir|0700, owner, group) {
		return bad(privateConfigurationError("DIRECTORY_CHANGED"))
	}
	if !ready {
		if action == "recover" {
			return bad(privateConfigurationError("INCOMPLETE_STAGE_PRESERVED"))
		}
		result.Status = "incomplete_stage_preserved"
		return result, nil
	}
	if !stage && !published {
		return bad(privateConfigurationError("JOURNAL_REFUSED"))
	}
	name := composePrivateStage
	if published {
		name = engine.UtilityPrivateDirectory
	}
	if e = verifyPrivateGeneration(ctx, p, name); e != nil {
		return bad(e)
	}
	if !unchanged() || !configurationDirectoryUnchanged(journal, journalBefore, os.ModeDir|0700, owner, group) {
		return bad(privateConfigurationError("DIRECTORY_CHANGED"))
	}
	if action == "recover" {
		if !committed {
			return publishPrivateGeneration(ctx, root, before, p, result, durability)
		}
		// Visible committed bytes can survive a previous Write/Sync/Close or journal
		// fsync failure. Explicit recovery must establish durability again, without
		// rewriting the marker, copying payloads or replaying the rename.
		if e = privateSyncFile(root, composePrivateCommitted, owner, group, durability); e != nil {
			return bad(e)
		}
		for _, dir := range []string{engine.UtilityPrivateDirectory, composePrivateJournal, "."} {
			if e = privateSync(root, dir, durability); e != nil {
				return bad(e)
			}
		}
		if !unchanged() || !configurationDirectoryUnchanged(journal, journalBefore, os.ModeDir|0700, owner, group) {
			return bad(privateConfigurationError("DIRECTORY_CHANGED"))
		}
		if ctx.Err() != nil {
			return bad(privateConfigurationError("PUBLICATION_UNCERTAIN"))
		}
	}
	result.Status = "ready_requires_explicit_recovery"
	if published {
		result.Status = "publication_requires_explicit_recovery"
	}
	if committed {
		result.Status = "observed_private_configuration"
		if action == "recover" {
			result.Status = "verified_private_configuration"
		}
	}
	result.Files = len(p.Files)
	for _, f := range p.Files {
		result.Bytes += f.Bytes
	}
	return result, nil
}

func verifyPrivateGeneration(ctx context.Context, p composeprofile.PrivateConfiguration, name string) error {
	root, before, e := pinConfigurationDirectory(filepath.Join(p.TargetRoot, name), os.ModeDir|0700, p.UID, p.GID)
	if e != nil {
		return e
	}
	defer root.Close()
	_, e = privatePayload(ctx, root, p, p.UID, p.GID)
	if e != nil {
		return e
	}
	if ctx.Err() != nil {
		return privateConfigurationError("INTERRUPTED")
	}
	if !configurationDirectoryUnchanged(root, before, os.ModeDir|0700, p.UID, p.GID) {
		return privateConfigurationError("DIRECTORY_CHANGED")
	}
	return nil
}

func publishPrivateGeneration(ctx context.Context, root *os.Root, before os.FileInfo, p composeprofile.PrivateConfiguration, result ComposePrivateResult, durability privateDurability) (ComposePrivateResult, error) {
	bad := func(e error) (ComposePrivateResult, error) { return ComposePrivateResult{}, e }
	if ctx.Err() != nil {
		return bad(privateConfigurationError("INTERRUPTED"))
	}
	if !configurationDirectoryUnchanged(root, before, os.ModeDir|0755, os.Geteuid(), os.Getegid()) {
		return bad(privateConfigurationError("DIRECTORY_CHANGED"))
	}
	// A previous fsync/response may have failed after bytes became visible.
	// Explicit recovery re-establishes durability instead of trusting visibility.
	for _, name := range []string{composePrivateIntent, composePrivateReady} {
		if e := privateSyncFile(root, name, os.Geteuid(), os.Getegid(), durability); e != nil {
			return bad(e)
		}
	}
	_, e := root.Lstat(engine.UtilityPrivateDirectory)
	if os.IsNotExist(e) {
		if e = verifyPrivateGeneration(ctx, p, composePrivateStage); e != nil {
			return bad(e)
		}
		if e = root.Rename(composePrivateStage, engine.UtilityPrivateDirectory); e != nil {
			return bad(privateConfigurationError("PUBLICATION_UNCERTAIN"))
		}
	} else if e != nil {
		return bad(privateConfigurationError("PUBLICATION_UNCERTAIN"))
	}
	// Rename never crosses devices: both names are under this pinned volume root.
	for _, dir := range []string{composePrivateJournal, "."} {
		if e = privateSync(root, dir, durability); e != nil {
			return bad(e)
		}
	}
	if e = verifyPrivateGeneration(ctx, p, engine.UtilityPrivateDirectory); e != nil {
		return bad(e)
	}
	for _, file := range p.Files {
		if ctx.Err() != nil {
			return bad(privateConfigurationError("PUBLICATION_UNCERTAIN"))
		}
		if e = privateSyncFile(root, engine.UtilityPrivateDirectory+"/"+file.Name, p.UID, p.GID, durability); e != nil {
			return bad(e)
		}
	}
	if e = privateSync(root, engine.UtilityPrivateDirectory, durability); e != nil {
		return bad(e)
	}
	if ctx.Err() != nil {
		return bad(privateConfigurationError("PUBLICATION_UNCERTAIN"))
	}
	if e = privateWrite(root, composePrivateCommitted, []byte(p.Fingerprint()+"\n"), os.Geteuid(), os.Getegid(), durability); e != nil {
		return bad(e)
	}
	if e = privateSync(root, composePrivateJournal, durability); e != nil {
		return bad(e)
	}
	if !configurationDirectoryUnchanged(root, before, os.ModeDir|0755, os.Geteuid(), os.Getegid()) {
		return bad(privateConfigurationError("DIRECTORY_CHANGED"))
	}
	if ctx.Err() != nil {
		return bad(privateConfigurationError("PUBLICATION_UNCERTAIN"))
	}
	result.Status = "verified_private_configuration"
	result.Files = len(p.Files)
	for _, f := range p.Files {
		result.Bytes += f.Bytes
	}
	return result, nil
}
