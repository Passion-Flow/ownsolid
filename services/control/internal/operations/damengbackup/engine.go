package damengbackup

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// State is an independent, complete native database readback, supplied by the
// installation owner under its continuous same-session maintenance fence.
// CatalogSHA256 binds definitions, constraints, grants and applied checksums;
// RowsSHA256 binds typed canonical every-row/LOB bytes, never just row counts.
type State struct {
	ServerID       string `json:"server_id"`
	Version        string `json:"version"`
	Schema         string `json:"schema"`
	Principal      string `json:"principal"`
	InstallationID string `json:"installation_id"`
	CatalogSHA256  string `json:"catalog_sha256"`
	RowsSHA256     string `json:"rows_sha256"`
	Objects        int64  `json:"objects"`
	EndpointSHA256 string `json:"endpoint_sha256"`
	FreshMarker    string `json:"fresh_marker"`
}

// Custody is borrowed; this package never commits, unlocks or closes it. The
// native composition must hold installation/schema/writer fences for the entire
// call and bind Inspect to the same actual socket/credentials. An ODBC transport
// probe alone does not qualify the dexp/dimp connection.
type Custody interface {
	CheckFence(context.Context) error
	Inspect(context.Context) (State, error)
}

type Intent struct {
	FormatVersion        int          `json:"format_version"`
	Kind                 string       `json:"kind"`
	Root                 string       `json:"root"`
	RootIdentity         FileIdentity `json:"root_identity"`
	CreatedUTC           string       `json:"created_utc"`
	Reason               string       `json:"reason"`
	ConfigurationSHA256  string       `json:"configuration_sha256"`
	InventorySHA256      string       `json:"inventory_sha256"`
	Policy               Policy       `json:"policy"`
	SocketIdentity       FileIdentity `json:"socket_identity"`
	EndpointSHA256       string       `json:"endpoint_sha256"`
	Original             State        `json:"original"`
	Source               State        `json:"source"`
	BackupRoot           string       `json:"backup_root"`
	BackupCompleteSHA256 string       `json:"backup_complete_sha256"`
	ArchiveSHA256        string       `json:"archive_sha256"`
	ArchiveBytes         int64        `json:"archive_bytes"`
}
type Dispatch struct {
	FormatVersion   int    `json:"format_version"`
	IntentSHA256    string `json:"intent_sha256"`
	ArgumentsSHA256 string `json:"arguments_sha256"`
}
type Joined struct {
	FormatVersion  int       `json:"format_version"`
	DispatchSHA256 string    `json:"dispatch_sha256"`
	Execution      Execution `json:"execution"`
}
type Complete struct {
	FormatVersion int    `json:"format_version"`
	IntentSHA256  string `json:"intent_sha256"`
	JoinedSHA256  string `json:"joined_sha256"`
	ArchiveSHA256 string `json:"archive_sha256"`
	ArchiveBytes  int64  `json:"archive_bytes"`
	Database      State  `json:"database"`
	// Database verification deliberately conveys no runtime access authority.
	DatabaseVerified bool `json:"database_verified"`
}

func (n *Native) validState(s State, fresh bool, endpoint string) bool {
	c := n.configuration.Connection
	if !validText(s.ServerID) || s.Version != n.inventory.ServerVersion || s.Schema != c.Schema || s.Principal != c.User || !digest.MatchString(s.CatalogSHA256) || !digest.MatchString(s.RowsSHA256) || s.EndpointSHA256 != endpoint || s.Objects < 0 {
		return false
	}
	if fresh {
		return s.Objects == 0 && s.InstallationID == "" && validText(s.FreshMarker)
	}
	return s.Objects > 0 && validText(s.InstallationID) && s.FreshMarker == ""
}
func sameData(a, b State) bool {
	return a.Version == b.Version && a.Schema == b.Schema && a.Principal == b.Principal && a.InstallationID == b.InstallationID && a.CatalogSHA256 == b.CatalogSHA256 && a.RowsSHA256 == b.RowsSHA256 && a.Objects == b.Objects
}
func (n *Native) bounded(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, time.Duration(n.configuration.Policy.OperationSeconds)*time.Second)
}
func (n *Native) binding() (FileIdentity, string, []byte, error) {
	if e := n.verifyServiceConfiguration(); e != nil {
		return FileIdentity{}, "", nil, e
	}
	socket, e := n.verifySocket()
	if e != nil {
		return socket, "", nil, e
	}
	password, e := readPrivate(n.configuration.Connection.PasswordFile, maximumPasswordBytes)
	// This first variant accepts only unambiguous printable ASCII password bytes.
	// Vendor escaping outside this alphabet remains an explicit unsupported input,
	// rather than guessed shell/CONFIG_FILE quoting or a default password fallback.
	if e != nil || len(password) < 1 {
		return socket, "", nil, fail("PASSWORD_INVALID")
	}
	for _, b := range password {
		if b < 33 || b > 126 || strings.ContainsRune(`()/@#{}"'\\`, rune(b)) {
			return socket, "", nil, fail("PASSWORD_ENCODING_UNSUPPORTED")
		}
	}
	endpoint := fingerprint(struct {
		Connection     Connection
		PasswordSHA256 string
		Socket         FileIdentity
	}{n.configuration.Connection, sha(password), socket})
	return socket, endpoint, password, nil
}

// EndpointFingerprint lets a real native owner compare its protected connector
// binding before furnishing State. This is a binding, not connectivity evidence.
func (n *Native) EndpointFingerprint() (string, error) {
	_, s, p, e := n.binding()
	clear(p)
	return s, e
}
func (n *Native) inspect(ctx context.Context, c Custody) (State, error) {
	if c == nil || c.CheckFence(ctx) != nil {
		return State{}, fail("CONTINUOUS_FENCE_REQUIRED")
	}
	s, e := c.Inspect(ctx)
	if e != nil || c.CheckFence(ctx) != nil {
		return State{}, fail("INSPECTION_FAILED")
	}
	return s, nil
}
func (n *Native) check(ctx context.Context, c Custody, i Intent) error {
	if ctx.Err() != nil {
		return fail("CANCELLED")
	}
	socket, endpoint, password, e := n.binding()
	clear(password)
	if e != nil || socket != i.SocketIdentity || endpoint != i.EndpointSHA256 || c.CheckFence(ctx) != nil {
		return fail("CUSTODY_CHANGED")
	}
	return nil
}
func (n *Native) preflight(ctx context.Context, path, reason string, c Custody) (Intent, *os.Root, []byte, error) {
	if n == nil || ctx == nil || c == nil || !canonical(path) || len(reason) < 1 || len(reason) > 500 || strings.ContainsAny(reason, "\x00\r\n") {
		return Intent{}, nil, nil, fail("INPUT_INVALID")
	}
	if n.workingDirectory != "" && len(filepath.Join(path, "database.dmp")) > 128 {
		return Intent{}, nil, nil, fail("NATIVE_OUTPUT_PATH_TOO_LONG")
	}
	if related(path, n.configuration.InventoryFile) || related(path, n.configuration.Connection.PasswordFile) || related(path, n.configuration.Connection.SocketFile) {
		return Intent{}, nil, nil, fail("ROOT_OVERLAP")
	}
	for _, a := range n.inventory.artifacts() {
		if related(path, a.Path) {
			return Intent{}, nil, nil, fail("ROOT_OVERLAP")
		}
	}
	if e := n.verify(ctx); e != nil {
		return Intent{}, nil, nil, e
	}
	socket, endpoint, password, e := n.binding()
	if e != nil {
		return Intent{}, nil, nil, e
	}
	defer clear(password)
	s, e := n.inspect(ctx, c)
	if e != nil {
		return Intent{}, nil, nil, e
	}
	if e = requireSpace(filepath.Dir(path), n.configuration.Policy.MaximumArchiveBytes+maximumDocumentBytes, n.configuration.Policy.MinimumFreeBytes); e != nil {
		return Intent{}, nil, nil, e
	}
	r, e := newRoot(path)
	if e != nil {
		return Intent{}, nil, nil, e
	}
	info, e := r.Stat(".")
	if e != nil {
		r.Close()
		return Intent{}, nil, nil, fail("ROOT_INVALID")
	}
	i := Intent{FormatVersion: FormatVersion, Root: path, RootIdentity: identity(info), CreatedUTC: time.Now().UTC().Format(time.RFC3339Nano), Reason: reason, ConfigurationSHA256: n.configurationFingerprint(), InventorySHA256: n.configuration.InventorySHA256, Policy: n.configuration.Policy, SocketIdentity: socket, EndpointSHA256: endpoint, Original: s}
	// Generated bytes remain exclusively in a 0600 file. The original protected
	// password reference is never copied into argv, environment or process output.
	connection := []byte("DUMP_CONN_STR=(" + n.configuration.Connection.User + "/" + string(password) + "@" + n.configuration.Connection.SocketFile + "#{inet_type=UNIXSOCKET})\n")
	return i, r, connection, nil
}
func (n *Native) arguments(i Intent) []string {
	args := []string{"CONFIG_FILE=" + filepath.Join(i.Root, "connection.conf"), "DIRECTORY=" + i.Root, "FILE=database.dmp", "SCHEMAS=" + n.configuration.Connection.Schema, "ROWS=Y", "INDEXES=Y", "CONSTRAINTS=Y", "TRIGGERS=Y", "GRANTS=Y", "DUMMY=N", "NOLOGFILE=Y", "PARALLEL=1"}
	if n.workingDirectory != "" {
		args[2] = "FILE=" + filepath.Join(i.Root, "database.dmp")
	}
	if i.Kind == "backup" {
		return append(args, "WITH_UR=N", "DROP=N", "COMPRESS=N", "FILENUM=1", archiveSizeArgument(i.Policy.MaximumArchiveBytes))
	}
	// TABLE_EXISTS_ACTION is deliberately omitted: its documented default is an
	// error. No SKIP/APPEND/TRUNCATE/REPLACE, remapping or ignored errors are allowed.
	return append(args, "IGNORE=N", "FAST_LOAD=N", "COMPILE=Y")
}
func (n *Native) dispatch(ctx context.Context, c Custody, r *os.Root, i Intent, connection []byte) (resultError error) {
	defer clear(connection)
	if e := writeNew(r, "intent.json", jsonBytes(i)); e != nil {
		return e
	}
	if e := writeNew(r, "connection.conf", connection); e != nil {
		return e
	}
	mayRemoveSecret := true
	// Cleanup is limited to the inventoried secret file, after process join. On a
	// crash the original protected file is retained for deliberate reconciliation.
	defer func() {
		clear(connection)
		if !mayRemoveSecret {
			resultError = errors.Join(resultError, fail("ACTIVE_CONSUMER_REQUIRES_INSPECTION"))
			return
		}
		if e := r.Remove("connection.conf"); e != nil {
			resultError = errors.Join(resultError, fail("SECRET_CLEANUP_REQUIRED"))
		} else if syncRoot(r) != nil {
			resultError = errors.Join(resultError, fail("SECRET_CLEANUP_REQUIRED"))
		}
	}()
	args := n.arguments(i)
	if e := n.check(ctx, c, i); e != nil {
		return e
	}
	before, e := n.inspect(ctx, c)
	if e != nil || before != i.Original {
		return fail("ORIGINAL_STATE_CHANGED")
	}
	d := Dispatch{FormatVersion, fingerprint(i), fingerprint(args)}
	if e = writeNew(r, "dispatch.json", jsonBytes(d)); e != nil {
		return e
	}
	tool := n.inventory.Dexp.Artifact.Path
	if i.Kind == "restore" {
		tool = n.inventory.Dimp.Artifact.Path
	}
	result, raw, runError := runProcess(ctx, tool, args, n.executionDirectory(i.Root), n.environment(), n.configuration.Policy, func() error {
		if e := n.check(ctx, c, i); e != nil {
			return e
		}
		info, e := r.Lstat("database.dmp")
		if e == nil && (!owned(info, false, false) || identity(info).UID != uint32(os.Geteuid()) || info.Size() > n.configuration.Policy.MaximumArchiveBytes) {
			return fail("ARCHIVE_BUDGET_EXCEEDED")
		}
		if e != nil && !os.IsNotExist(e) {
			return fail("ARCHIVE_OBSERVATION_FAILED")
		}
		return requireSpace(i.Root, 0, n.configuration.Policy.MinimumFreeBytes)
	})
	mayRemoveSecret = !result.Started || result.ProcessGroupStopped
	clear(raw)
	// A post-Start failure, timeout, output cap or unavailable joined receipt is
	// unknown external outcome. Even a known nonzero exit can leave partial SQL.
	joinedError := writeNew(r, "joined.json", jsonBytes(Joined{FormatVersion, fingerprint(d), result}))
	if joinedError != nil {
		return fail("RESULT_DURABILITY_UNCERTAIN")
	}
	if runError != nil {
		if result.Started {
			return fail("OUTCOME_UNKNOWN")
		}
		return fail("NOT_STARTED")
	}
	return nil
}

// Backup uses a new root and never retries an original dispatch. The caller
// must additionally capture every owning store/keyring under the same fence to
// create a matched platform recovery set; this result seals the database only.
func (n *Native) Backup(ctx context.Context, path, reason string, c Custody) (Complete, error) {
	if n == nil || ctx == nil {
		return Complete{}, fail("INPUT_INVALID")
	}
	ctx, cancel := n.bounded(ctx)
	defer cancel()
	i, r, connection, e := n.preflight(ctx, path, reason, c)
	if e != nil {
		return Complete{}, e
	}
	defer r.Close()
	i.Kind = "backup"
	i.Source = i.Original
	if !n.validState(i.Source, false, i.EndpointSHA256) {
		clear(connection)
		return Complete{}, fail("SOURCE_INVALID")
	}
	if e = n.dispatch(ctx, c, r, i, connection); e != nil {
		return Complete{}, e
	}
	return n.reconcile(ctx, r, c, i)
}

func (n *Native) Restore(ctx context.Context, backupRoot, path, reason string, c Custody) (Complete, error) {
	if n == nil || ctx == nil || !canonical(backupRoot) || related(backupRoot, path) {
		return Complete{}, fail("INPUT_INVALID")
	}
	ctx, cancel := n.bounded(ctx)
	defer cancel()
	b, e := openPrivateRoot(backupRoot)
	if e != nil {
		return Complete{}, e
	}
	defer b.Close()
	var bi Intent
	if _, e = readDocument(b, "intent.json", &bi); e != nil || bi.Kind != "backup" || bi.Root != backupRoot {
		return Complete{}, fail("BACKUP_INTENT_INVALID")
	}
	bc, bcRaw, e := n.readComplete(b, bi)
	if e != nil {
		return Complete{}, e
	}
	if _, _, e = readArchive(ctx, b, n.configuration.Policy.MaximumArchiveBytes, bc.ArchiveSHA256); e != nil {
		return Complete{}, e
	}
	i, r, connection, e := n.preflight(ctx, path, reason, c)
	if e != nil {
		return Complete{}, e
	}
	defer r.Close()
	defer clear(connection)
	i.Kind = "restore"
	i.Source = bc.Database
	i.BackupRoot = backupRoot
	i.BackupCompleteSHA256 = sha(bcRaw)
	i.ArchiveSHA256 = bc.ArchiveSHA256
	i.ArchiveBytes = bc.ArchiveBytes
	if !n.validState(i.Original, true, i.EndpointSHA256) || !sameSourceProfile(i.Source, i.Original) || i.Source.ServerID == i.Original.ServerID {
		return Complete{}, fail("FRESH_DISTINCT_TARGET_REQUIRED")
	}
	if e = copyArchive(ctx, b, r, bc.ArchiveBytes, bc.ArchiveSHA256); e != nil {
		return Complete{}, e
	}
	if e = n.dispatch(ctx, c, r, i, connection); e != nil {
		return Complete{}, e
	}
	return n.reconcile(ctx, r, c, i)
}
func sameSourceProfile(a, b State) bool {
	return a.Version == b.Version && a.Schema == b.Schema && a.Principal == b.Principal
}
func copyArchive(ctx context.Context, source, target *os.Root, size int64, expected string) error {
	f, e := source.Open("database.dmp")
	if e != nil {
		return fail("ARCHIVE_REQUIRED")
	}
	defer f.Close()
	i, e := f.Stat()
	if e != nil || !owned(i, true, false) || i.Size() != size {
		return fail("ARCHIVE_INVALID")
	}
	out, e := target.OpenFile("database.dmp", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return fail("FRESH_ARCHIVE_REQUIRED")
	}
	h := newHash()
	copied, e := io.Copy(io.MultiWriter(out, h), io.LimitReader(contextReader{ctx, f}, size+1))
	se := out.Sync()
	ce := out.Close()
	if e != nil || se != nil || ce != nil || copied != size || hashString(h) != expected {
		return fail("ARCHIVE_COPY_FAILED")
	}
	return syncRoot(target)
}
