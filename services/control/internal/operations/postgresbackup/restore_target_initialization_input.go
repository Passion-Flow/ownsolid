package postgresbackup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const targetInitializationPolicyPrefix = "PROJECT_ONE_POSTGRES_TARGET_INITIALIZATION_"

func applyTargetInitializationPolicy(p *release.PostgreSQLRestoreTargetInitializationPolicy, raw []byte) error {
	bad := errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_POLICY_INVALID")
	if p == nil || bytes.ContainsAny(raw, "\x00\r") {
		return bad
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || value == "" || strings.TrimSpace(line) != line || seen[key] {
			return bad
		}
		seen[key] = true
		n, e := strconv.ParseInt(value, 10, 64)
		if e != nil || strconv.FormatInt(n, 10) != value {
			return bad
		}
		switch key {
		case targetInitializationPolicyPrefix + "OPERATION_SECONDS":
			if n < 1 || n > 300 {
				return bad
			}
			p.OperationSeconds = int(n)
		case targetInitializationPolicyPrefix + "CLEANUP_SECONDS":
			if n < 1 || n > 30 {
				return bad
			}
			p.CleanupSeconds = int(n)
		case targetInitializationPolicyPrefix + "MAXIMUM_RECORD_BYTES":
			p.MaximumRecordBytes = n
		case targetInitializationPolicyPrefix + "MINIMUM_FREE_BYTES":
			p.MinimumFreeBytes = n
		default:
			return bad
		}
	}
	if !p.Valid() {
		return bad
	}
	return nil
}

func LoadRestoreTargetInitializationRequest(path, policyEnvironment string) (release.PostgreSQLRestoreTargetInitializationRequest, string, error) {
	var r release.PostgreSQLRestoreTargetInitializationRequest
	if _, e := readPrivateDocument(path, MaximumRequestBytes, &r); e != nil {
		return r, "", e
	}
	if policyEnvironment != "" {
		raw, e := readPrivateBytes(policyEnvironment, MaximumRequestBytes)
		if e != nil || applyTargetInitializationPolicy(&r.Policy, raw) != nil {
			return release.PostgreSQLRestoreTargetInitializationRequest{}, "", errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_POLICY_INVALID")
		}
	}
	if !r.ValidForInspection() {
		return release.PostgreSQLRestoreTargetInitializationRequest{}, "", errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_REQUEST_INVALID")
	}
	return r, r.SHA256(), nil
}

type targetInitializationInputs struct {
	sha256  string
	records []nativeReferenceRecord
}

func captureTargetInitializationInputs(ctx context.Context, c config.Config, operatorPath, operatorEnvironment, requestPath, policyEnvironment string, r release.PostgreSQLRestoreTargetInitializationRequest, selected *pgx.ConnConfig) (targetInitializationInputs, error) {
	var out targetInitializationInputs
	bad := errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_INPUTS_INVALID")
	if ctx == nil || ctx.Err() != nil || !r.ValidForInspection() || c.DatabaseEngine() != config.PostgreSQLDatabaseEngine || c.Database.MaxOpenConnections < 2 || c.Database.MaxIdleConnections > 1 {
		return out, bad
	}
	if e := RefuseAmbient(); e != nil {
		return out, e
	}
	if c.Server.ApplicationProfile != nil && *c.Server.ApplicationProfile != r.ApplicationProfile {
		return out, bad
	}
	loaded, e := config.LoadProtectedWithEnvironment(operatorPath, operatorEnvironment)
	if e != nil || !reflect.DeepEqual(loaded, c) {
		return out, bad
	}
	request, _, e := LoadRestoreTargetInitializationRequest(requestPath, policyEnvironment)
	if e != nil || request != r {
		return out, bad
	}
	// This is only a typed connection-context projection for the original URI
	// admission helper, never a fabricated backup request or schema authority.
	connectionContext := release.PostgreSQLBackupRequest{Database: r.TargetDatabase, Operator: r.TargetOperator, Policy: release.PostgreSQLBackupPolicy{OperationSeconds: r.Policy.OperationSeconds}}
	connection, e := readConnection(c.Database.DSNFile, connectionContext, selected)
	if e != nil || connection.host != r.Endpoint.Host || connection.port != strconv.Itoa(r.Endpoint.Port) || connection.sslmode != r.Endpoint.SSLMode {
		return out, bad
	}
	files := []struct {
		role, path string
		maximum    int64
	}{{"operator", operatorPath, 1 << 20}, {"request", requestPath, MaximumRequestBytes}, {"operator-dsn", c.Database.DSNFile, 16 << 10}}
	for _, file := range []struct{ role, path string }{{"operator-environment", operatorEnvironment}, {"policy-environment", policyEnvironment}, {"tls-ca", connection.ca}, {"tls-certificate", connection.certificate}, {"tls-key", connection.key}} {
		if file.path != "" {
			files = append(files, struct {
				role, path string
				maximum    int64
			}{file.role, file.path, 1 << 20})
		}
	}
	fingerprints := map[string]string{}
	for _, file := range files {
		if relatedPaths(r.InitializationRoot, file.path) {
			return out, bad
		}
		proof, e := fileDigest(ctx, file.path, file.maximum)
		if e != nil {
			return out, bad
		}
		fingerprints[file.role] = proof.SHA256
		out.records = append(out.records, nativeReferenceRecord{file.path, file.maximum, false})
	}
	for _, path := range storeRoots(c) {
		if path != "" && relatedPaths(path, r.InitializationRoot) {
			return out, bad
		}
	}
	raw, e := json.Marshal(c)
	if e != nil {
		return out, bad
	}
	fingerprints["effective-operator-configuration"] = digest(raw)
	fingerprints["effective-request"] = r.SHA256()
	raw, e = canonical(fingerprints)
	if e != nil {
		return out, bad
	}
	out.sha256 = digest(raw)
	return out, nil
}

// This admission never opens SQL or creates a directory. In particular an
// existing initialization root refuses mutation before opening the DB owner.
func AdmitRestoreTargetInitialization(ctx context.Context, c config.Config, operatorPath, operatorEnvironment, requestPath, policyEnvironment string, r release.PostgreSQLRestoreTargetInitializationRequest, inspection bool) error {
	if !inspection && !r.Valid() {
		return errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_PIN_REQUIRED")
	}
	if _, e := captureTargetInitializationInputs(ctx, c, operatorPath, operatorEnvironment, requestPath, policyEnvironment, r, nil); e != nil {
		return e
	}
	parent, e := openPrivateRoot(filepath.Dir(r.InitializationRoot))
	if e != nil {
		return e
	}
	defer parent.Close()
	info, e := os.Lstat(r.InitializationRoot)
	if !inspection {
		if !os.IsNotExist(e) {
			return errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_ALREADY_STARTED")
		}
		return requireSpace(filepath.Dir(r.InitializationRoot), 2*r.Policy.MaximumRecordBytes+4096, r.Policy.MinimumFreeBytes)
	}
	if e == nil && !owned(info, true) || e != nil && !os.IsNotExist(e) {
		return errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_JOURNAL_REFUSED")
	}
	return nil
}
