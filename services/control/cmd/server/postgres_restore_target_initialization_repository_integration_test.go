//go:build repository_integration && postgres_target_initialization_integration

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/postgresbackup"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
	trainingwire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

const targetInitializationFixtureRoot = "/fixture-target"

type targetInitializationFixtureCase struct {
	Name          string `json:"name"`
	Configuration string `json:"operator_configuration_file"`
	Request       string `json:"request_file"`
}
type targetInitializationFixtureSelector struct {
	Format string                            `json:"format"`
	Cases  []targetInitializationFixtureCase `json:"cases"`
}
type targetInitializationFixtureOutput struct{ bytes.Buffer }

func (b *targetInitializationFixtureOutput) Write(raw []byte) (int, error) {
	if len(raw) > (64<<10)-b.Len() {
		return 0, errors.New("TARGET_INITIALIZATION_FIXTURE_OUTPUT_BOUND")
	}
	return b.Buffer.Write(raw)
}
func targetInitializationFixtureSHA(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func targetInitializationFixturePath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && (path == targetInitializationFixtureRoot || strings.HasPrefix(path, targetInitializationFixtureRoot+"/"))
}
func targetInitializationFixtureOwned(info os.FileInfo, directory bool) bool {
	if info == nil {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Uid == 65534 && ((directory && info.Mode() == os.ModeDir|0700) || (!directory && info.Mode() == 0600 && st.Nlink == 1))
}
func targetInitializationFixtureRootAt(path string) (*os.Root, error) {
	bad := errors.New("TARGET_INITIALIZATION_FIXTURE_PRIVATE_ROOT_REQUIRED")
	canonical, e := filepath.EvalSymlinks(path)
	before, be := os.Lstat(path)
	if !targetInitializationFixturePath(path) || e != nil || canonical != path || be != nil || !targetInitializationFixtureOwned(before, true) {
		return nil, bad
	}
	root, e := os.OpenRoot(path + string(filepath.Separator) + ".")
	if e != nil {
		return nil, bad
	}
	after, e := root.Stat(".")
	if e != nil || !targetInitializationFixtureOwned(after, true) || !os.SameFile(before, after) {
		root.Close()
		return nil, bad
	}
	return root, nil
}
func targetInitializationFixtureRead(path string, maximum int64) ([]byte, error) {
	bad := errors.New("TARGET_INITIALIZATION_FIXTURE_PRIVATE_FILE_REQUIRED")
	if !targetInitializationFixturePath(path) || maximum < 1 || maximum > config.MaximumProtectedConfigurationBytes {
		return nil, bad
	}
	root, e := targetInitializationFixtureRootAt(filepath.Dir(path))
	if e != nil {
		return nil, bad
	}
	defer root.Close()
	name := filepath.Base(path)
	before, e := root.Lstat(name)
	if e != nil || !targetInitializationFixtureOwned(before, false) || before.Size() < 1 || before.Size() > maximum {
		return nil, bad
	}
	f, e := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, bad
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil || !os.SameFile(before, opened) {
		return nil, bad
	}
	raw, e := io.ReadAll(io.LimitReader(f, maximum+1))
	after, ae := f.Stat()
	named, ne := root.Lstat(name)
	parent, pe := os.Lstat(filepath.Dir(path))
	pinned, ie := root.Stat(".")
	canonical, ce := filepath.EvalSymlinks(filepath.Dir(path))
	if e != nil || ae != nil || ne != nil || pe != nil || ie != nil || ce != nil || canonical != filepath.Dir(path) || !os.SameFile(parent, pinned) || !targetInitializationFixtureOwned(parent, true) || !targetInitializationFixtureOwned(after, false) || !targetInitializationFixtureOwned(named, false) || !os.SameFile(before, after) || !os.SameFile(before, named) || before.Size() != int64(len(raw)) || after.Size() != before.Size() || named.Size() != before.Size() || !before.ModTime().Equal(after.ModTime()) || !before.ModTime().Equal(named.ModTime()) {
		return nil, bad
	}
	return raw, nil
}
func targetInitializationFixtureSync(path string) error {
	root, e := targetInitializationFixtureRootAt(path)
	if e != nil {
		return e
	}
	defer root.Close()
	f, e := root.Open(".")
	if e != nil {
		return e
	}
	e = f.Sync()
	return errors.Join(e, f.Close())
}
func targetInitializationFixtureWrite(path string, raw []byte) error {
	if !targetInitializationFixturePath(path) || len(raw) < 1 || len(raw) > config.MaximumProtectedConfigurationBytes {
		return errors.New("TARGET_INITIALIZATION_FIXTURE_WRITE_REFUSED")
	}
	root, e := targetInitializationFixtureRootAt(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer root.Close()
	f, e := root.OpenFile(filepath.Base(path), os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return e
	}
	n, e := f.Write(raw)
	if e == nil && n != len(raw) {
		e = io.ErrShortWrite
	}
	if e == nil {
		e = f.Sync()
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return errors.New("TARGET_INITIALIZATION_FIXTURE_WRITE_UNCERTAIN")
	}
	return targetInitializationFixtureSync(filepath.Dir(path))
}
func targetInitializationFixtureJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, e := json.Marshal(value)
	if e != nil || targetInitializationFixtureWrite(path, append(raw, '\n')) != nil {
		t.Fatal("exclusive protected fixture JSON publication failed")
	}
}
func targetInitializationFixtureAbsent(t *testing.T, path string) {
	t.Helper()
	if _, e := os.Lstat(path); !os.IsNotExist(e) {
		t.Fatal("fixture path must remain absent", path)
	}
}
func targetInitializationFixtureInvoke(t *testing.T, command, configuration, request string, success bool) release.PostgreSQLRestoreTargetInitializationObservation {
	t.Helper()
	var output targetInitializationFixtureOutput
	e := runPostgresTargetInitialization(command, []string{"--config", configuration, "--request", request}, &output)
	if !success {
		if e == nil || output.Len() != 0 {
			t.Fatal("original helper must refuse without stdout")
		}
		return release.PostgreSQLRestoreTargetInitializationObservation{}
	}
	if e != nil {
		t.Fatal("original target initialization helper failed", safeError(e))
	}
	var observation release.PostgreSQLRestoreTargetInitializationObservation
	r, _, e := postgresbackup.LoadRestoreTargetInitializationRequest(request, "")
	if e != nil || json.Unmarshal(output.Bytes(), &observation) != nil || !observation.ValidFor(r, command == postgresTargetInspectCommand) {
		t.Fatal("actual helper observation invalid")
	}
	return observation
}

// The parent provisions four distinct disposable PostgreSQL18.6 databases and
// independently compares negative-case SQL preimages. This test creates no SQL
// pool or database and invokes only the original selected-pool CLI helper.
func TestActualPostgresRestoreTargetInitializationOriginalHelperAndUnknownCommit(t *testing.T) {
	if os.Geteuid() != 65534 {
		t.Fatal("exact nonroot fixture owner required")
	}
	selectorFile := os.Getenv("PROJECT_ONE_TEST_POSTGRES_TARGET_INITIALIZATION_INPUT_FILE")
	selectorRaw, e := targetInitializationFixtureRead(selectorFile, 64<<10)
	var selector targetInitializationFixtureSelector
	if e != nil || trainingwire.DecodeStrict(selectorRaw, &selector) != nil || selector.Format != "projectone.postgres.target-initialization-fixture.v1" || len(selector.Cases) != 4 {
		t.Fatal("explicit protected four-case selector required")
	}
	cases := map[string]targetInitializationFixtureCase{}
	requests := map[string]release.PostgreSQLRestoreTargetInitializationRequest{}
	originals := map[string]string{}
	databases, roots := map[string]bool{}, map[string]bool{}
	for _, entry := range selector.Cases {
		if entry.Name != "fresh" && entry.Name != "foreign-marker" && entry.Name != "nonempty" && entry.Name != "unknown-commit" {
			t.Fatal("fixed fixture case required")
		}
		if _, exists := cases[entry.Name]; exists {
			t.Fatal("duplicate fixture case")
		}
		raw, e := targetInitializationFixtureRead(entry.Request, 64<<10)
		r, _, re := postgresbackup.LoadRestoreTargetInitializationRequest(entry.Request, "")
		if e != nil || re != nil || !targetInitializationFixturePath(entry.Configuration) || !targetInitializationFixturePath(r.InitializationRoot) || (entry.Name == "fresh") == r.IdentityPinned() || databases[r.TargetDatabase] || roots[r.InitializationRoot] {
			t.Fatal("exact distinct fixture input required")
		}
		if _, e = config.LoadProtectedWithEnvironment(entry.Configuration, ""); e != nil {
			t.Fatal("protected operator configuration required")
		}
		cases[entry.Name], requests[entry.Name], originals[entry.Request] = entry, r, targetInitializationFixtureSHA(raw)
		databases[r.TargetDatabase], roots[r.InitializationRoot] = true, true
		targetInitializationFixtureAbsent(t, r.InitializationRoot)
	}
	observations := map[string]any{}
	t.Run("fresh", func(t *testing.T) {
		entry, original := cases["fresh"], requests["fresh"]
		discovery := targetInitializationFixtureInvoke(t, postgresTargetInspectCommand, entry.Configuration, entry.Request, true)
		if discovery.State != "uninitialized" || discovery.IdentityPinned || discovery.OriginalIntentPresent || discovery.OriginalCompletionPresent || discovery.EffectObserved {
			t.Fatal("readonly unpinned discovery must remain uninitialized")
		}
		targetInitializationFixtureAbsent(t, original.InitializationRoot)
		pinned := original
		pinned.ExpectedSystemIdentifier, pinned.ExpectedDatabaseOID = discovery.Target.SystemIdentifier, discovery.Target.DatabaseOID
		requestFile := filepath.Join(filepath.Dir(entry.Request), "pinned-request.json")
		targetInitializationFixtureJSON(t, requestFile, pinned)
		initialized := targetInitializationFixtureInvoke(t, postgresTargetInitializeCommand, entry.Configuration, requestFile, true)
		if !initialized.EffectObserved || !initialized.OriginalCompletionPresent || initialized.ReadOnly {
			t.Fatal("actual once-only initialization completion required")
		}
		completeFile := filepath.Join(pinned.InitializationRoot, "complete.json")
		complete, e := targetInitializationFixtureRead(completeFile, pinned.Policy.MaximumRecordBytes)
		if e != nil || targetInitializationFixtureSHA(complete) != initialized.CompletionSHA256 {
			t.Fatal("original durable completion required")
		}
		targetInitializationFixtureInvoke(t, postgresTargetInitializeCommand, entry.Configuration, requestFile, false)
		inspected := targetInitializationFixtureInvoke(t, postgresTargetInspectCommand, entry.Configuration, requestFile, true)
		if !inspected.OriginalCompletionPresent || inspected.Target.BackendPID == initialized.Target.BackendPID || inspected.IntentSHA256 != initialized.IntentSHA256 || inspected.CompletionSHA256 != initialized.CompletionSHA256 {
			t.Fatal("new backend must reopen original completion")
		}
		retained := filepath.Join(filepath.Dir(entry.Request), "retained-original-completion.json")
		targetInitializationFixtureAbsent(t, retained)
		if os.Rename(completeFile, retained) != nil || targetInitializationFixtureSync(pinned.InitializationRoot) != nil || targetInitializationFixtureSync(filepath.Dir(retained)) != nil {
			t.Fatal("retain original completion outside journal")
		}
		missing := targetInitializationFixtureInvoke(t, postgresTargetInspectCommand, entry.Configuration, requestFile, true)
		if !missing.EffectObserved || !missing.OriginalIntentPresent || missing.OriginalCompletionPresent || missing.CompletionSHA256 != "" || missing.Target.BackendPID == inspected.Target.BackendPID || missing.IntentSHA256 != initialized.IntentSHA256 {
			t.Fatal("readonly observer must preserve missing completion")
		}
		targetInitializationFixtureAbsent(t, completeFile)
		actual, e := targetInitializationFixtureRead(retained, pinned.Policy.MaximumRecordBytes)
		if e != nil || !bytes.Equal(actual, complete) {
			t.Fatal("retained original completion bytes changed")
		}
		observations["fresh"] = map[string]any{"discovery": discovery, "initialized": initialized, "new_backend_inspection": inspected, "missing_completion_inspection": missing, "retained_original_completion_file": retained}
	})
	for _, name := range []string{"foreign-marker", "nonempty"} {
		t.Run(name, func(t *testing.T) {
			entry, request := cases[name], requests[name]
			targetInitializationFixtureInvoke(t, postgresTargetInitializeCommand, entry.Configuration, entry.Request, false)
			targetInitializationFixtureAbsent(t, request.InitializationRoot)
			observations[name] = "original helper refused without stdout or journal; independent SQL preimage comparison belongs to parent"
		})
	}
	t.Run("unknown-commit", func(t *testing.T) {
		entry, request := cases["unknown-commit"], requests["unknown-commit"]
		configurationFile, requestFile, proxy := targetInitializationFixtureProxyInputs(t, entry, request)
		var output targetInitializationFixtureOutput
		e := runPostgresTargetInitialization(postgresTargetInitializeCommand, []string{"--config", configurationFile, "--request", requestFile}, &output)
		if e == nil || !strings.Contains(e.Error(), "POSTGRES_RESTORE_TARGET_INITIALIZATION_COMMIT_UNKNOWN") || output.Len() != 0 || proxy.commits.Load() != 1 || proxy.dropped.Load() != 1 {
			t.Fatal("actual server COMMIT acknowledgement must be lost exactly once")
		}
		targetInitializationFixtureAbsent(t, filepath.Join(request.InitializationRoot, "complete.json"))
		intent, e := targetInitializationFixtureRead(filepath.Join(request.InitializationRoot, "intent.json"), request.Policy.MaximumRecordBytes)
		if e != nil {
			t.Fatal("unknown commit must retain original intent")
		}
		inspected := targetInitializationFixtureInvoke(t, postgresTargetInspectCommand, configurationFile, requestFile, true)
		if !inspected.EffectObserved || !inspected.OriginalIntentPresent || inspected.OriginalCompletionPresent || inspected.CompletionSHA256 != "" || inspected.IntentSHA256 != targetInitializationFixtureSHA(intent) || proxy.dropped.Load() != 1 || proxy.overflow.Load() {
			t.Fatal("readonly same-input observation of committed effect required")
		}
		targetInitializationFixtureAbsent(t, filepath.Join(request.InitializationRoot, "complete.json"))
		observations["unknown-commit"] = map[string]any{"inspection": inspected, "server_commit_acknowledgements_dropped": proxy.dropped.Load(), "initialization_never_resent": true, "fault": "Real PostgreSQL local TCP response loss after backend CommandComplete(COMMIT); no protocol response is fabricated."}
	})
	if t.Failed() {
		return
	}
	for path, sha := range originals {
		raw, e := targetInitializationFixtureRead(path, 64<<10)
		if e != nil || targetInitializationFixtureSHA(raw) != sha {
			t.Fatal("original request changed")
		}
	}
	current, e := targetInitializationFixtureRead(selectorFile, 64<<10)
	if e != nil || !bytes.Equal(current, selectorRaw) {
		t.Fatal("original selector changed")
	}
	targetInitializationFixtureJSON(t, filepath.Join(targetInitializationFixtureRoot, "actual-target-initialization-receipt.json"), map[string]any{"format": "projectone.postgres.target-initialization-fixture-receipt.v1", "selector_sha256": targetInitializationFixtureSHA(selectorRaw), "cases": observations, "qualification": "Actual PostgreSQL18.6 and original CLI helper on four parent-owned disposable databases, including local TCP COMMIT response loss. No Scope/Engine, image, MVP service, GPU or physical-target qualification. Raw requests and original completion retained; no marker retry after unknown commit."})
}

func targetInitializationFixtureProxyInputs(t *testing.T, entry targetInitializationFixtureCase, request release.PostgreSQLRestoreTargetInitializationRequest) (string, string, *targetInitializationCommitProxy) {
	t.Helper()
	c, e := config.LoadProtectedWithEnvironment(entry.Configuration, "")
	if e != nil || request.Endpoint.Host != "127.0.0.1" || request.Endpoint.SSLMode != "disable" {
		t.Fatal("explicit local plaintext disposable fault endpoint required")
	}
	raw, e := targetInitializationFixtureRead(c.Database.DSNFile, 16<<10)
	u, ue := url.Parse(strings.TrimSpace(string(raw)))
	if e != nil || ue != nil || u == nil || u.User == nil || u.Hostname() != request.Endpoint.Host || u.Port() != strconv.Itoa(request.Endpoint.Port) || u.Query().Get("sslmode") != "disable" {
		t.Fatal("protected original fault connection required")
	}
	proxy := newTargetInitializationCommitProxy(t, net.JoinHostPort(request.Endpoint.Host, strconv.Itoa(request.Endpoint.Port)))
	port := proxy.listener.Addr().(*net.TCPAddr).Port
	rootPath := filepath.Join(filepath.Dir(entry.Request), "proxy-input")
	parent, e := targetInitializationFixtureRootAt(filepath.Dir(rootPath))
	if e != nil {
		t.Fatal("protected fault input parent")
	}
	e = parent.Mkdir(filepath.Base(rootPath), 0700)
	parent.Close()
	if e != nil || targetInitializationFixtureSync(filepath.Dir(rootPath)) != nil {
		t.Fatal("exclusive fault input directory")
	}
	u.Host = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	dsnFile := filepath.Join(rootPath, "operator-dsn")
	if targetInitializationFixtureWrite(dsnFile, []byte(u.String()+"\n")) != nil {
		t.Fatal("protected proxied DSN publication")
	}
	c.Database.DSNFile, c.Database.MaxIdleConnections = dsnFile, 0
	configurationFile := filepath.Join(rootPath, "operator.yaml")
	originalConfiguration, e := targetInitializationFixtureRead(entry.Configuration, config.MaximumProtectedConfigurationBytes)
	if e != nil {
		t.Fatal("original protected configuration snapshot required")
	}
	var document yaml.Node
	if yaml.Unmarshal(originalConfiguration, &document) != nil || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		t.Fatal("original configuration mapping required")
	}
	rebased := false
	for i := 0; i < len(document.Content[0].Content); i += 2 {
		if document.Content[0].Content[i].Value != "database" {
			continue
		}
		database := document.Content[0].Content[i+1]
		if database.Kind != yaml.MappingNode {
			t.Fatal("original database mapping required")
		}
		for n := 0; n < len(database.Content); n += 2 {
			if database.Content[n].Value == "dsn_file" {
				database.Content[n+1].Value = dsnFile
				database.Content[n+1].Tag = "!!str"
				rebased = true
			}
		}
	}
	if !rebased {
		t.Fatal("exact original DSN reference required")
	}
	// Preserve original explicit/omitted typed inputs. Serializing the complete
	// effective Config can materialize defaults and change its input contract.
	configurationRaw, e := yaml.Marshal(&document)
	if e != nil || targetInitializationFixtureWrite(configurationFile, configurationRaw) != nil {
		t.Fatal("protected typed proxied configuration publication")
	}
	loaded, e := config.LoadProtectedWithEnvironment(configurationFile, "")
	if e != nil {
		t.Fatal("proxied typed configuration readback", safeError(e))
	}
	if !reflect.DeepEqual(loaded, c) {
		var changed []string
		a, b := reflect.ValueOf(loaded), reflect.ValueOf(c)
		for i := 0; i < a.NumField(); i++ {
			if !reflect.DeepEqual(a.Field(i).Interface(), b.Field(i).Interface()) {
				changed = append(changed, a.Type().Field(i).Name)
			}
		}
		t.Fatal("proxied typed configuration fields differ", changed)
	}
	request.Endpoint.Port = port
	requestFile := filepath.Join(rootPath, "request.json")
	targetInitializationFixtureJSON(t, requestFile, request)
	return configurationFile, requestFile, proxy
}
