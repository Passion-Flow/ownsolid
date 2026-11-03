package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"path"
)

// ReadNativePostgreSQLTaskCompletion reads one ORIGINAL durable completion from
// an exact successful terminal task. It performs only GETs, including when the
// original attach response was lost. It never creates a missing completion.
// The caller persists these private bytes before removing the task or volumes.
func (c *Client) ReadNativePostgreSQLTaskCompletion(ctx context.Context, t DatabaseTask, id string) (TaskOutputObservation, error) {
	t = t.freezeNativeRecovery()
	bad := failure("OFFLINE_ENGINE_NATIVE_COMPLETION_READBACK_FAILED")
	if !t.validNativePostgreSQLRecovery() || t.Kind != NativePostgreSQLBackup && t.Kind != NativePostgreSQLRestore {
		return TaskOutputObservation{}, bad
	}
	before, e := c.exactDatabaseTask(ctx, t, id)
	if e != nil {
		return TaskOutputObservation{}, e
	}
	if before.State.Status != "exited" || before.State.Running || before.State.PID != 0 || before.State.ExitCode != 0 || before.State.OOMKilled {
		return TaskOutputObservation{}, bad
	}
	if e := c.databaseTaskNetwork(ctx, t, id, true); e != nil {
		return TaskOutputObservation{}, e
	}
	_, backup, records, _ := t.nativeRecoveryPaths()
	root := backup
	if t.Kind == NativePostgreSQLRestore {
		root = records
	}
	raw, status, e := c.request(ctx, http.MethodGet, "/v"+c.policy.APIVersion+"/containers/"+id+"/archive", url.Values{"path": {path.Join(root, "complete.json")}}, nil, c.config.RequestSeconds)
	if e != nil || status != http.StatusOK {
		return TaskOutputObservation{}, bad
	}
	body, e := nativePostgreSQLCompletionArchive(raw, t.UID, t.GID, t.nativeRecoveryPolicy().MaximumManifestBytes)
	if e != nil {
		return TaskOutputObservation{}, bad
	}
	out, e := decodeNativePostgreSQLRecoveryOutput(body, t)
	if e != nil {
		return TaskOutputObservation{}, bad
	}
	after, e := c.exactDatabaseTask(ctx, t, id)
	if e != nil || before.State != after.State {
		return TaskOutputObservation{}, bad
	}
	if e := c.databaseTaskNetwork(ctx, t, id, true); e != nil {
		return TaskOutputObservation{}, e
	}
	return out, nil
}

func nativePostgreSQLCompletionArchive(raw []byte, uid, gid int, maximum int64) ([]byte, error) {
	bad := failure("OFFLINE_ENGINE_NATIVE_COMPLETION_ARCHIVE_INVALID")
	if maximum < 1 || len(raw) < 3*tarBlockBytes || len(raw)%tarBlockBytes != 0 || len(bytes.Trim(raw[len(raw)-tarEndBytes:], "\x00")) != 0 {
		return nil, bad
	}
	source := bytes.NewReader(raw)
	reader := tar.NewReader(source)
	h, e := reader.Next()
	if e != nil || h.Name != "complete.json" || h.Typeflag != tar.TypeReg || h.Linkname != "" || h.Mode != 0600 || h.Uid != uid || h.Gid != gid || h.Size < 1 || h.Size > maximum || len(h.PAXRecords) > 0 || len(h.Xattrs) > 0 {
		return nil, bad
	}
	body, e := io.ReadAll(io.LimitReader(reader, h.Size+1))
	if e != nil || int64(len(body)) != h.Size {
		return nil, bad
	}
	if _, e = reader.Next(); e != io.EOF || len(bytes.Trim(raw[len(raw)-source.Len():], "\x00")) != 0 {
		return nil, bad
	}
	return body, nil
}
