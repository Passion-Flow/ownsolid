package security

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func normalizationPasswords(t *testing.T) *Passwords {
	t.Helper()
	dir := t.TempDir()
	if os.Chmod(dir, 0700) != nil {
		t.Fatal("NORMALIZATION_PRIVATE_FIXTURE_FAILED")
	}
	path := filepath.Join(dir, "blocklist")
	if os.WriteFile(path, []byte("passwordpassword\n"), 0600) != nil {
		t.Fatal("NORMALIZATION_BLOCKLIST_FIXTURE_FAILED")
	}
	p, e := NewPasswords(path)
	if e != nil {
		t.Fatal("NORMALIZATION_ACTUAL_CONSTRUCTOR_FAILED", e)
	}
	return p
}

func TestM01PasswordCanonicalBoundaries(t *testing.T) {
	p := normalizationPasswords(t)
	for _, v := range []struct{ name, raw, canonical string }{
		{"latin_decomposition_rune_boundary", strings.Repeat("e\u0301", 128), strings.Repeat("\u00e9", 128)},
		{"hangul_decomposition_byte_boundary", strings.Repeat("\u1100\u1161\u11a8", 128), strings.Repeat("\uac01", 128)},
		{"maximum_utf8_bytes", strings.Repeat("🧪", 128), strings.Repeat("🧪", 128)},
		{"significant_edge_spaces", " " + strings.Repeat("a", 126) + " ", " " + strings.Repeat("a", 126) + " "},
	} {
		t.Run(v.name, func(t *testing.T) {
			normalized, e := p.Policy(v.raw, "alice@example.invalid", "Test installation")
			if e != nil || normalized != v.canonical {
				t.Fatal("CANONICAL_PASSWORD_POLICY_DISAGREES")
			}
			phc, e := p.Hash(t.Context(), v.raw)
			if e != nil || !strings.HasPrefix(phc, "$argon2id$v=19$m=19456,t=2,p=1$") {
				t.Fatal("CANONICAL_PASSWORD_HASH_FAILED_OR_COST_CHANGED")
			}
			for _, supplied := range []string{v.raw, v.canonical} {
				if ok, e := p.Verify(t.Context(), phc, supplied); e != nil || !ok {
					t.Fatal("CREATED_CANONICAL_PASSWORD_CANNOT_AUTHENTICATE")
				}
			}
			runes := []rune(v.canonical)
			runes[len(runes)-1] = 'Z'
			if ok, e := p.Verify(t.Context(), phc, string(runes)); e != nil || ok {
				t.Fatal("PASSWORD_LAST_CHARACTER_TRUNCATED_OR_VERIFY_FAILED")
			}
			if v.name == "significant_edge_spaces" {
				if ok, e := p.Verify(t.Context(), phc, strings.TrimSpace(v.raw)); e != nil || ok {
					t.Fatal("PASSWORD_EDGE_SPACES_TRIMMED")
				}
			}
		})
	}
	if _, e := p.Policy(strings.Repeat("a", 14), "alice@example.invalid", "Test installation"); e == nil || e.Error() != "PASSWORD_POLICY_VIOLATION" {
		t.Fatal("PASSWORD_BELOW_MINIMUM_ACCEPTED")
	}
	if _, e := p.Policy(strings.Repeat("a", 15), "alice@example.invalid", "Test installation"); e != nil {
		t.Fatal("PASSWORD_MINIMUM_OR_NO_COMPOSITION_RULE_REJECTED")
	}
}

func TestM01InvalidPasswordBeforeHashAdmission(t *testing.T) {
	p := normalizationPasswords(t)
	phc, e := p.Hash(context.Background(), "Synthetic boundary credential")
	if e != nil {
		t.Fatal("PASSWORD_BOUNDARY_REAL_PHC_FIXTURE_FAILED")
	}
	// Explicit unit-only full-channel fixture. This is not actual worker evidence.
	for range cap(p.slots) {
		p.slots <- struct{}{}
	}
	for range cap(p.queue) {
		p.queue <- struct{}{}
	}
	for _, invalid := range []string{strings.Repeat("a", 129), strings.Repeat("🧪", 129), strings.Repeat("a", 4097), string([]byte{0xff, 0xfe})} {
		if _, e := p.Policy(invalid, "alice@example.invalid", "Test installation"); e == nil || e.Error() != "PASSWORD_POLICY_VIOLATION" {
			t.Fatal("INVALID_PASSWORD_POLICY_ACCEPTED")
		}
		if _, e := p.Hash(t.Context(), invalid); e == nil || e.Error() != "PASSWORD_POLICY_VIOLATION" {
			t.Fatal("INVALID_PASSWORD_REACHED_HASH_ADMISSION")
		}
		if ok, e := p.Verify(t.Context(), phc, invalid); e != nil || ok {
			t.Fatal("INVALID_PASSWORD_REACHED_VERIFY_ADMISSION")
		}
		if len(p.slots) != cap(p.slots) || len(p.queue) != cap(p.queue) {
			t.Fatal("INVALID_PASSWORD_CHANGED_IDENTIFIED_FIXTURE_OCCUPANCY")
		}
	}
	t.Log("INVALID_PASSWORD_BEFORE_ADMISSION policy_hash_verify=true fixture=identified_manual_full_channels no_actual_worker_claim=true")
}
