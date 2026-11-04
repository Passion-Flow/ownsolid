package postgresbackup

import (
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const restoreRootsInspectionPolicyPrefix = "PROJECT_ONE_POSTGRES_ROOTS_INSPECTION_"

// This operation owns checked closes for input directories as well as files.
// Keep the existing private protocol while avoiding an ignored parent Close in
// the older generic reference reader. No input or store is opened for writing.
func withRestoreRootsInspectionFile(ctx context.Context, record nativeReferenceRecord, consume func([]byte) error) (result error) {
	bad := errors.New("POSTGRES_RESTORE_ROOTS_INSPECTION_INPUT_CHANGED")
	if ctx == nil || ctx.Err() != nil || record.maximum < 1 || consume == nil {
		return bad
	}
	root, e := openPrivateRoot(filepath.Dir(record.path))
	if e != nil {
		return bad
	}
	defer func() { result = errors.Join(result, root.Close()) }()
	file, e := root.OpenFile(filepath.Base(record.path), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return bad
	}
	defer func() { result = errors.Join(result, file.Close()) }()
	before, e := file.Stat()
	if e != nil || !owned(before, false) || before.Size() < 1 || before.Size() > record.maximum {
		return bad
	}
	stamp, e := observeTree(before)
	if e != nil {
		return bad
	}
	raw, e := io.ReadAll(io.LimitReader(file, record.maximum+1))
	if e != nil || int64(len(raw)) != before.Size() {
		return bad
	}
	result = consume(raw)
	current, e := file.Stat()
	named, ne := root.Lstat(filepath.Base(record.path))
	parent, pe := os.Lstat(filepath.Dir(record.path))
	opened, oe := root.Stat(".")
	if e != nil || ne != nil || pe != nil || oe != nil || !owned(named, false) || !owned(parent, true) || !os.SameFile(parent, opened) || !os.SameFile(before, named) {
		return errors.Join(result, bad)
	}
	cs, ce := observeTree(current)
	ns, nse := observeTree(named)
	resolved, re := filepath.EvalSymlinks(record.path)
	if ce != nil || nse != nil || cs != stamp || ns != stamp || re != nil || resolved != record.path || ctx.Err() != nil {
		return errors.Join(result, bad)
	}
	if _, e = file.Seek(0, io.SeekStart); e != nil {
		return errors.Join(result, bad)
	}
	check, e := io.ReadAll(io.LimitReader(file, record.maximum+1))
	if e != nil || !bytes.Equal(raw, check) {
		return errors.Join(result, bad)
	}
	current, e = file.Stat()
	named, ne = root.Lstat(filepath.Base(record.path))
	if e != nil || ne != nil {
		return errors.Join(result, bad)
	}
	cs, ce = observeTree(current)
	ns, nse = observeTree(named)
	if ce != nil || nse != nil || cs != stamp || ns != stamp || !os.SameFile(before, named) {
		return errors.Join(result, bad)
	}
	return result
}

func readRestoreRootsInspectionBytes(path string, maximum int64) (raw []byte, result error) {
	result = withRestoreRootsInspectionFile(context.Background(), nativeReferenceRecord{path, maximum, false}, func(value []byte) error { raw = value; return nil })
	if result != nil {
		raw = nil
	}
	return raw, result
}

func withRestoreRootsInspectionFiles(ctx context.Context, records []nativeReferenceRecord, consume func() error) error {
	if len(records) == 0 {
		return consume()
	}
	return withRestoreRootsInspectionFile(ctx, records[0], func([]byte) error { return withRestoreRootsInspectionFiles(ctx, records[1:], consume) })
}

func applyRestoreRootsInspectionPolicy(p *release.PostgreSQLRestoreRootsInspectionPolicy, raw []byte) error {
	bad := errors.New("POSTGRES_RESTORE_ROOTS_INSPECTION_POLICY_INVALID")
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
		case restoreRootsInspectionPolicyPrefix + "OPERATION_SECONDS":
			if n < 1 || n > 300 {
				return bad
			}
			p.OperationSeconds = int(n)
		case restoreRootsInspectionPolicyPrefix + "MAXIMUM_RECORD_BYTES":
			p.MaximumRecordBytes = n
		default:
			return bad
		}
	}
	if !p.Valid() {
		return bad
	}
	return nil
}

func LoadRestoreRootsInspectionRequest(path, policyEnvironment string) (release.PostgreSQLRestoreRootsInspectionRequest, string, error) {
	var r release.PostgreSQLRestoreRootsInspectionRequest
	raw, e := readRestoreRootsInspectionBytes(path, MaximumRequestBytes)
	if e != nil || strict.DecodeStrict(raw, &r) != nil {
		return r, "", errors.New("POSTGRES_RESTORE_ROOTS_INSPECTION_REQUEST_INVALID")
	}
	if policyEnvironment != "" {
		original := r.Policy
		raw, e := readRestoreRootsInspectionBytes(policyEnvironment, MaximumRequestBytes)
		if e != nil || applyRestoreRootsInspectionPolicy(&r.Policy, raw) != nil {
			return release.PostgreSQLRestoreRootsInspectionRequest{}, "", errors.New("POSTGRES_RESTORE_ROOTS_INSPECTION_POLICY_INVALID")
		}
		if r.Policy != original {
			return release.PostgreSQLRestoreRootsInspectionRequest{}, "", errors.New("POSTGRES_RESTORE_ROOTS_INSPECTION_POLICY_CONTRADICTION")
		}
	}
	if !r.Valid() {
		return r, "", errors.New("POSTGRES_RESTORE_ROOTS_INSPECTION_REQUEST_INVALID")
	}
	return r, r.SHA256(), nil
}

func restoreRootsInspectionRecords(configuration, environment, request, policyEnvironment string) []nativeReferenceRecord {
	out := []nativeReferenceRecord{{configuration, config.MaximumProtectedConfigurationBytes, false}, {request, MaximumRequestBytes, false}}
	for _, path := range []string{environment, policyEnvironment} {
		if path != "" {
			out = append(out, nativeReferenceRecord{path, MaximumRequestBytes, false})
		}
	}
	return out
}

func captureRestoreRootsInspection(ctx context.Context, configuration, environment, request, policyEnvironment string, r release.PostgreSQLRestoreRootsInspectionRequest) (config.Config, error) {
	bad := errors.New("POSTGRES_RESTORE_ROOTS_INSPECTION_INPUTS_INVALID")
	if ctx == nil || ctx.Err() != nil || !r.Valid() || r.ExpectedUID != uint32(os.Geteuid()) || r.ExpectedGID != uint32(os.Getegid()) || !contained(r.RuntimeConfigurationRoot, configuration) || environment != "" && !contained(r.RuntimeConfigurationRoot, environment) || (environment == "") != (r.RuntimeEnvironmentSHA256 == "") {
		return config.Config{}, bad
	}
	loaded, _, e := LoadRestoreRootsInspectionRequest(request, policyEnvironment)
	if e != nil || !reflect.DeepEqual(loaded, r) {
		return config.Config{}, bad
	}
	raw, e := readRestoreRootsInspectionBytes(configuration, config.MaximumProtectedConfigurationBytes)
	if e != nil || digest(raw) != r.RuntimeConfigurationSHA256 {
		return config.Config{}, bad
	}
	if environment != "" {
		raw, e = readRestoreRootsInspectionBytes(environment, MaximumRequestBytes)
		if e != nil || digest(raw) != r.RuntimeEnvironmentSHA256 {
			return config.Config{}, bad
		}
	}
	c, e := config.LoadProtectedWithEnvironment(configuration, environment)
	if e != nil || c.DatabaseEngine() != config.PostgreSQLDatabaseEngine || r.ApplicationProfile != database.LineageName {
		return config.Config{}, bad
	}
	if validateStoreRoots(c, r.RuntimeConfigurationRoot) != nil {
		return config.Config{}, bad
	}
	for _, path := range storeRoots(c) {
		for _, input := range []string{request, policyEnvironment} {
			if input != "" && relatedPaths(path, input) {
				return config.Config{}, bad
			}
		}
	}
	return c, nil
}
