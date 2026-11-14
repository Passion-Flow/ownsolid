package postgresbackup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"reflect"
	"strconv"
	"strings"

	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const MaximumRequestBytes = 64 << 10
const environmentPrefix = "PROJECT_ONE_POSTGRES_BACKUP_"

func digest(raw []byte) string { v := sha256.Sum256(raw); return hex.EncodeToString(v[:]) }

// LoadRequest admits one private immutable request and an optional explicit
// policy overlay. The returned digest binds the effective request, not secrets.
func LoadRequest(path, environment string) (release.PostgreSQLBackupRequest, string, error) {
	var r release.PostgreSQLBackupRequest
	_, e := readPrivateDocument(path, MaximumRequestBytes, &r)
	if e != nil {
		return r, "", e
	}
	if environment != "" {
		raw, e := readPrivateBytes(environment, MaximumRequestBytes)
		if e != nil || applyEnvironment(&r.Policy, raw) != nil {
			return release.PostgreSQLBackupRequest{}, "", errors.New("POSTGRES_BACKUP_POLICY_ENVIRONMENT_INVALID")
		}
	}
	if !r.Valid() {
		return release.PostgreSQLBackupRequest{}, "", errors.New("POSTGRES_BACKUP_REQUEST_INVALID")
	}
	raw, e := canonical(r)
	if e != nil {
		return release.PostgreSQLBackupRequest{}, "", e
	}
	return r, digest(raw), nil
}

func applyEnvironment(p *release.PostgreSQLBackupPolicy, raw []byte) error {
	return applyPolicyEnvironment(p, raw, environmentPrefix)
}

func applyPolicyEnvironment(p *release.PostgreSQLBackupPolicy, raw []byte, prefix string) error {
	bad := errors.New("POSTGRES_BACKUP_POLICY_ENVIRONMENT_INVALID")
	if p == nil || bytes.ContainsAny(raw, "\x00\r") {
		return bad
	}
	v := reflect.ValueOf(p).Elem()
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
		found := false
		for i := 0; i < v.NumField(); i++ {
			field, _, _ := strings.Cut(v.Type().Field(i).Tag.Get("json"), ",")
			if key != prefix+strings.ToUpper(field) {
				continue
			}
			found = true
			f := v.Field(i)
			if f.Kind() == reflect.String {
				f.SetString(value)
				break
			}
			n, e := strconv.ParseInt(value, 10, f.Type().Bits())
			if e != nil || strconv.FormatInt(n, 10) != value {
				return bad
			}
			f.SetInt(n)
			break
		}
		if !found {
			return bad
		}
	}
	if !p.Valid() {
		return bad
	}
	return nil
}

// Refuse libpq/pgx ambient configuration before any database or native process.
// Neither the shared environment nor HOME/account state is changed.
func RefuseAmbient() error {
	for _, v := range os.Environ() {
		key, _, _ := strings.Cut(v, "=")
		if strings.HasPrefix(key, "PG") {
			return errors.New("POSTGRES_BACKUP_AMBIENT_DATABASE_CONFIGURATION_REFUSED")
		}
	}
	return nil
}
