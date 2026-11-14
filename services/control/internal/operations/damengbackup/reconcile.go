package damengbackup

import (
	"context"
	"os"
	"strconv"
)

func archiveSizeArgument(size int64) string { return "FILESIZE=" + strconv.FormatInt(size, 10) + "B" }

func (n *Native) readJoined(r *os.Root, i Intent) (Joined, []byte, error) {
	var d Dispatch
	var j Joined
	dRaw, e := readDocument(r, "dispatch.json", &d)
	if e != nil || d.FormatVersion != FormatVersion || d.IntentSHA256 != fingerprint(i) || d.ArgumentsSHA256 != fingerprint(n.arguments(i)) {
		return j, nil, fail("ORIGINAL_DISPATCH_REQUIRED")
	}
	jRaw, e := readDocument(r, "joined.json", &j)
	if e != nil {
		return j, nil, fail("OUTCOME_UNKNOWN")
	}
	if j.FormatVersion != FormatVersion || j.DispatchSHA256 != sha(dRaw) || !j.Execution.Started || j.Execution.ProcessGroupID < 1 || !j.Execution.ProcessGroupStopped || j.Execution.ExitCode != 0 || j.Execution.Cancelled || j.Execution.OutputLimited || j.Execution.Code != "JOINED_SUCCESS" || !digest.MatchString(j.Execution.OutputSHA256) || j.Execution.OutputBytes < 0 || j.Execution.OutputBytes > int64(i.Policy.MaximumOutputBytes) {
		return j, nil, fail("OUTCOME_UNKNOWN")
	}
	return j, jRaw, nil
}
func (n *Native) readComplete(r *os.Root, i Intent) (Complete, []byte, error) {
	var c Complete
	_, joinedRaw, e := n.readJoined(r, i)
	if e != nil {
		return c, nil, e
	}
	raw, e := readDocument(r, "complete.json", &c)
	if e != nil || i.Policy.Validate() != nil || i.InventorySHA256 != n.configuration.InventorySHA256 || c.FormatVersion != FormatVersion || c.IntentSHA256 != fingerprint(i) || c.JoinedSHA256 != sha(joinedRaw) || !digest.MatchString(c.ArchiveSHA256) || c.ArchiveBytes < 1 || c.ArchiveBytes > n.configuration.Policy.MaximumArchiveBytes || !c.DatabaseVerified || !sameData(c.Database, i.Source) {
		return Complete{}, nil, fail("COMPLETION_INVALID")
	}
	info, e := r.Stat(".")
	if e != nil || identity(info) != i.RootIdentity {
		return Complete{}, nil, fail("ROOT_IDENTITY_CHANGED")
	}
	if i.Kind == "backup" && (c.Database != i.Source || i.Source != i.Original) || i.Kind == "restore" && (c.Database.ServerID != i.Original.ServerID || c.Database.EndpointSHA256 != i.EndpointSHA256) {
		return Complete{}, nil, fail("COMPLETION_IDENTITY_INVALID")
	}
	return c, raw, nil
}
func (n *Native) validateIntent(r *os.Root, i Intent) error {
	info, e := r.Stat(".")
	if e != nil || identity(info) != i.RootIdentity || i.FormatVersion != FormatVersion || (i.Kind != "backup" && i.Kind != "restore") || i.ConfigurationSHA256 != n.configurationFingerprint() || i.InventorySHA256 != n.configuration.InventorySHA256 || i.Policy != n.configuration.Policy || !canonical(i.Root) || !digest.MatchString(i.EndpointSHA256) || !sameSourceProfile(i.Source, i.Original) {
		return fail("ORIGINAL_INTENT_MISMATCH")
	}
	if i.Kind == "backup" && (i.Source != i.Original || i.BackupRoot != "" || i.BackupCompleteSHA256 != "" || i.ArchiveSHA256 != "" || i.ArchiveBytes != 0 || !n.validState(i.Source, false, i.EndpointSHA256)) {
		return fail("ORIGINAL_INTENT_MISMATCH")
	}
	if i.Kind == "restore" && (!n.validState(i.Original, true, i.EndpointSHA256) || i.Source.ServerID == i.Original.ServerID || !canonical(i.BackupRoot) || related(i.Root, i.BackupRoot) || !digest.MatchString(i.BackupCompleteSHA256) || !digest.MatchString(i.ArchiveSHA256) || i.ArchiveBytes < 1 || i.ArchiveBytes > n.configuration.Policy.MaximumArchiveBytes) {
		return fail("ORIGINAL_INTENT_MISMATCH")
	}
	return nil
}

// Reconcile performs no export/import and never removes an original dispatch.
// It may publish completion only from a retained joined-success receipt and a
// complete current native readback. Missing/nonzero/cancelled joins stay unknown.
func (n *Native) Reconcile(ctx context.Context, path string, c Custody) (Complete, error) {
	if n == nil || ctx == nil || c == nil {
		return Complete{}, fail("INPUT_INVALID")
	}
	ctx, cancel := n.bounded(ctx)
	defer cancel()
	r, e := openPrivateRoot(path)
	if e != nil {
		return Complete{}, e
	}
	defer r.Close()
	var i Intent
	if _, e = readDocument(r, "intent.json", &i); e != nil || i.Root != path {
		return Complete{}, fail("ORIGINAL_INTENT_REQUIRED")
	}
	return n.reconcile(ctx, r, c, i)
}
func (n *Native) reconcile(ctx context.Context, r *os.Root, c Custody, i Intent) (Complete, error) {
	if e := n.validateIntent(r, i); e != nil {
		return Complete{}, e
	}
	if e := n.verifyArtifacts(ctx); e != nil {
		return Complete{}, e
	}
	_, joinedRaw, e := n.readJoined(r, i)
	if e != nil {
		return Complete{}, e
	}
	// No active process is admitted for an original root. A secret file left by
	// an abrupt parent death requires operator process/descriptor inspection;
	// reconciliation cannot prove it is safe to delete or publish completion.
	if _, e = r.Lstat("connection.conf"); !os.IsNotExist(e) {
		return Complete{}, fail("SECRET_OR_ACTIVE_CONSUMER_REQUIRES_INSPECTION")
	}
	if e = n.check(ctx, c, i); e != nil {
		return Complete{}, e
	}
	state, e := n.inspect(ctx, c)
	if e != nil {
		return Complete{}, e
	}
	if i.Kind == "backup" && state != i.Source {
		return Complete{}, fail("SOURCE_CHANGED")
	}
	if i.Kind == "restore" && (!n.validState(state, false, i.EndpointSHA256) || state.ServerID != i.Original.ServerID || !sameData(state, i.Source)) {
		return Complete{}, fail("RESTORE_VERIFICATION_FAILED")
	}
	if i.Kind == "restore" {
		b, e := openPrivateRoot(i.BackupRoot)
		if e != nil {
			return Complete{}, e
		}
		var bi Intent
		_, e = readDocument(b, "intent.json", &bi)
		if e != nil || bi.Kind != "backup" || bi.Root != i.BackupRoot {
			b.Close()
			return Complete{}, fail("BACKUP_INTENT_INVALID")
		}
		bc, raw, e := n.readComplete(b, bi)
		if e != nil || sha(raw) != i.BackupCompleteSHA256 || bc.Database != i.Source || bc.ArchiveSHA256 != i.ArchiveSHA256 || bc.ArchiveBytes != i.ArchiveBytes {
			b.Close()
			return Complete{}, fail("BACKUP_CHANGED")
		}
		_, size, e := readArchive(ctx, b, n.configuration.Policy.MaximumArchiveBytes, i.ArchiveSHA256)
		b.Close()
		if e != nil || size != i.ArchiveBytes {
			return Complete{}, fail("BACKUP_CHANGED")
		}
	}
	hash, size, e := archive(ctx, r, n.configuration.Policy.MaximumArchiveBytes, i.ArchiveSHA256)
	if e != nil {
		return Complete{}, e
	}
	if i.Kind == "restore" && size != i.ArchiveBytes {
		return Complete{}, fail("ARCHIVE_CHANGED")
	}
	if e = n.check(ctx, c, i); e != nil {
		return Complete{}, e
	}
	// Hashes cover the original sealed sources, not a newly supplied replacement.
	complete := Complete{FormatVersion, fingerprint(i), sha(joinedRaw), hash, size, state, true}
	if _, e = r.Lstat("complete.json"); e == nil {
		old, _, e := n.readComplete(r, i)
		if e != nil || old != complete {
			return Complete{}, fail("COMPLETION_CHANGED")
		}
		return old, nil
	} else if !os.IsNotExist(e) {
		return Complete{}, fail("COMPLETION_INVALID")
	}
	if e = writeNew(r, "complete.json", jsonBytes(complete)); e != nil {
		return Complete{}, e
	}
	read, _, e := n.readComplete(r, i)
	if e != nil || read != complete {
		return Complete{}, fail("COMPLETION_READBACK_FAILED")
	}
	return read, nil
}
