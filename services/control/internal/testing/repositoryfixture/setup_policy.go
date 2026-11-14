//go:build repository_integration

package repositoryfixture

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"unicode/utf8"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlmigrate"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// This reference is test-only and is consumed once by the fixture setup owner.
const setupPolicyEnvironment = "PROJECT_ONE_TEST_REPOSITORY_SETUP_FILE"
const maximumSetupPolicyBytes = 4096

type setupPolicy struct {
	FormatVersion int              `json:"format_version"`
	SetupSeconds  int              `json:"setup_seconds"`
	MySQL         mysqlSetupPolicy `json:"mysql"`
}
type mysqlSetupPolicy struct {
	LockWaitSeconds int `json:"lock_wait_seconds"`
	StepSeconds     int `json:"step_seconds"`
	TotalSeconds    int `json:"total_seconds"`
	CleanupSeconds  int `json:"cleanup_seconds"`
}

func defaultSetupPolicy() setupPolicy {
	return setupPolicy{FormatVersion: 1, SetupSeconds: 30, MySQL: mysqlSetupPolicy{LockWaitSeconds: 1, StepSeconds: 5, TotalSeconds: 20, CleanupSeconds: 2}}
}
func (p setupPolicy) mysqlOptions(steps int) mysqlmigrate.Options {
	return mysqlmigrate.Options{LockWaitSeconds: p.MySQL.LockWaitSeconds, StepSeconds: p.MySQL.StepSeconds, TotalSeconds: p.MySQL.TotalSeconds, CleanupSeconds: p.MySQL.CleanupSeconds, MaximumSteps: steps, MaximumMetadataBytes: 1 << 20}
}
func setupPolicyFailure() error { return errors.New("REPOSITORY_SETUP_POLICY_INVALID") }

func loadSetupPolicy() (setupPolicy, error) {
	reference, present := os.LookupEnv(setupPolicyEnvironment)
	if !present {
		return defaultSetupPolicy(), nil
	}
	if reference == "" {
		return setupPolicy{}, setupPolicyFailure()
	}
	// This test reference also requires its operator-owned private directory;
	// the shared reader below owns the bounded regular-file/descriptor checks.
	parent := filepath.Dir(reference)
	canonical, e := filepath.EvalSymlinks(parent)
	private := func(info os.FileInfo) bool {
		if info == nil || info.Mode() != os.ModeDir|0700 {
			return false
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		return ok && uint64(st.Uid) == uint64(os.Geteuid())
	}
	before, statError := os.Lstat(parent)
	if e != nil || canonical != parent || statError != nil || !private(before) {
		return setupPolicy{}, setupPolicyFailure()
	}
	// Reuse the existing bounded, pinned same-UID private configuration reader.
	// Its outer read cap is1MiB; this policy further admits at most4KiB JSON.
	raw, e := bundle.ReadProtectedConfiguration(reference)
	after, statError := os.Lstat(parent)
	if e != nil || statError != nil || !private(after) || !os.SameFile(before, after) {
		return setupPolicy{}, setupPolicyFailure()
	}
	return decodeSetupPolicy(raw)
}
func decodeSetupPolicy(raw []byte) (setupPolicy, error) {
	var p setupPolicy
	if len(raw) == 0 || len(raw) > maximumSetupPolicyBytes || !utf8.Valid(raw) || wire.StrictJSON(raw, &p) != nil {
		return setupPolicy{}, setupPolicyFailure()
	}
	// encoding/json's field matching is case-insensitive. Admit only exact
	// first-party keys as well as the shared duplicate/null/unknown checks.
	var top map[string]json.RawMessage
	var mysql map[string]json.RawMessage
	if json.Unmarshal(raw, &top) != nil || len(top) != 3 || top["format_version"] == nil || top["setup_seconds"] == nil || top["mysql"] == nil || json.Unmarshal(top["mysql"], &mysql) != nil || len(mysql) != 4 || mysql["lock_wait_seconds"] == nil || mysql["step_seconds"] == nil || mysql["total_seconds"] == nil || mysql["cleanup_seconds"] == nil {
		return setupPolicy{}, setupPolicyFailure()
	}
	// Existing migration validation owns its step/lock/cleanup bounds. Setup
	// also leaves time for the owning lock cleanup; business deadlines are not
	// inputs to this policy and never receive this context.
	if p.FormatVersion != 1 || p.SetupSeconds < 30 || p.SetupSeconds > 900 || p.mysqlOptions(1).Validate() != nil || p.SetupSeconds < p.MySQL.TotalSeconds+p.MySQL.CleanupSeconds+p.MySQL.LockWaitSeconds {
		return setupPolicy{}, setupPolicyFailure()
	}
	return p, nil
}
