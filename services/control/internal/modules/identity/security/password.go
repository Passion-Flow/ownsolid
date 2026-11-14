package security

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	"golang.org/x/text/unicode/norm"
)

// These bounds belong to the versioned credential format, not deployment settings.
const (
	passwordMinimumRunes    = 15
	passwordMaximumRunes    = 128
	passwordMaximumBytes    = 512
	passwordSaltBytes       = 16
	passwordKeyBytes        = 32
	argonMemoryKiB          = 19456
	argonIterations         = 2
	argonParallelism        = 1
	argonMaximumMemoryKiB   = 131072
	argonMaximumIterations  = 10
	argonMaximumParallelism = 4
)

// MaximumPasswordInputBytes is the existing wire safety bound before NFC.
// Credential rune/byte limits apply to the normalized value, never raw NFD.
const MaximumPasswordInputBytes = 4096

// Email accepts only ASCII, so the OpenAPI character ceiling is also bytes.
const maximumEmailInputBytes = 320

func normalizedPassword(v string) (string, bool) {
	if len(v) > MaximumPasswordInputBytes || !utf8.ValidString(v) {
		return "", false
	}
	v = norm.NFC.String(v)
	if len(v) > passwordMaximumBytes || utf8.RuneCountInString(v) > passwordMaximumRunes {
		return "", false
	}
	return v, true
}

var emailLocal = regexp.MustCompile("^[a-zA-Z0-9!#$%&'*+/=?^_`{|}~.-]+$")
var domainLabel = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

func Email(v string) (string, string, error) {
	if len(v) > maximumEmailInputBytes {
		return "", "", errors.New("VALIDATION_FAILED")
	}
	display := strings.Trim(v, " \t\r\n")
	// Validate before Unicode case mapping can turn unsupported input into ASCII.
	for _, r := range display {
		if r > unicode.MaxASCII {
			return "", "", errors.New("VALIDATION_FAILED")
		}
	}
	v = strings.ToLower(display)
	if len(v) > 254 || len(v) == 0 || strings.Count(v, "@") != 1 {
		return "", "", errors.New("VALIDATION_FAILED")
	}
	pair := strings.Split(v, "@")
	if len(pair[0]) > 64 || !emailLocal.MatchString(pair[0]) || strings.HasPrefix(pair[0], ".") || strings.HasSuffix(pair[0], ".") || strings.Contains(pair[0], "..") {
		return "", "", errors.New("VALIDATION_FAILED")
	}
	labels := strings.Split(pair[1], ".")
	if len(labels) < 2 {
		return "", "", errors.New("VALIDATION_FAILED")
	}
	for _, l := range labels {
		if !domainLabel.MatchString(l) {
			return "", "", errors.New("VALIDATION_FAILED")
		}
	}
	return v, display, nil
}
func Name(v string) (string, error) {
	v = norm.NFC.String(strings.TrimSpace(v))
	n := utf8.RuneCountInString(v)
	if !utf8.ValidString(v) || n < 1 || n > 100 || len(v) > 400 {
		return "", errors.New("VALIDATION_FAILED")
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return "", errors.New("VALIDATION_FAILED")
		}
	}
	return v, nil
}
func Locale(v string) bool { return v == "en" || v == "zh-CN" }

type Passwords struct {
	blocked      map[string]bool
	slots, queue chan struct{}
	Dummy        string
	admission    time.Duration
}

func NewPasswords(path string, policies ...config.IdentityPolicy) (*Passwords, error) {
	policy := config.DefaultRuntimePolicy().Identity
	if len(policies) > 0 {
		policy = policies[0]
	}
	check := config.DefaultRuntimePolicy()
	check.Identity = policy
	if e := check.Validate(); e != nil {
		return nil, e
	}
	b, e := os.ReadFile(path)
	if e != nil || len(b) == 0 {
		return nil, errors.New("BLOCKLIST_MISSING")
	}
	p := &Passwords{blocked: map[string]bool{}, slots: make(chan struct{}, policy.HashWorkers), queue: make(chan struct{}, policy.HashWorkers+policy.HashQueueCapacity), admission: config.Milliseconds(policy.HashAdmissionMilliseconds)}
	for _, line := range strings.Split(string(b), "\n") {
		if line != "" {
			p.blocked[norm.NFC.String(line)] = true
		}
	}
	if len(p.blocked) == 0 {
		return nil, errors.New("BLOCKLIST_EMPTY")
	}
	p.Dummy, e = p.Hash(context.Background(), "Synthetic dummy credential never accepted")
	return p, e
}
func (p *Passwords) Policy(v, email, installation string) (string, error) {
	v, valid := normalizedPassword(v)
	if !valid || utf8.RuneCountInString(v) < passwordMinimumRunes {
		return "", errors.New("PASSWORD_POLICY_VIOLATION")
	}
	local := strings.Split(email, "@")[0]
	for _, deny := range []string{email, local, installation} {
		if deny != "" && strings.EqualFold(v, deny) {
			return "", errors.New("PASSWORD_BLOCKED")
		}
	}
	if p.blocked[v] {
		return "", errors.New("PASSWORD_BLOCKED")
	}
	return v, nil
}
func (p *Passwords) acquire(ctx context.Context) (func(), error) {
	select {
	case p.queue <- struct{}{}:
	default:
		return nil, errors.New("AUTH_CAPACITY_EXCEEDED")
	}
	timer := time.NewTimer(p.admission)
	defer timer.Stop()
	select {
	case p.slots <- struct{}{}:
		return func() { <-p.slots; <-p.queue }, nil
	case <-ctx.Done():
		<-p.queue
		return nil, errors.New("AUTH_CAPACITY_EXCEEDED")
	case <-timer.C:
		<-p.queue
		return nil, errors.New("AUTH_CAPACITY_EXCEEDED")
	}
}
func (p *Passwords) Hash(ctx context.Context, v string) (string, error) {
	v, valid := normalizedPassword(v)
	if !valid {
		return "", errors.New("PASSWORD_POLICY_VIOLATION")
	}
	release, e := p.acquire(ctx)
	if e != nil {
		return "", e
	}
	defer release()
	salt := make([]byte, passwordSaltBytes)
	if _, e = rand.Read(salt); e != nil {
		return "", e
	}
	key := argon2.IDKey([]byte(v), salt, argonIterations, argonMemoryKiB, argonParallelism, passwordKeyBytes)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemoryKiB, argonIterations, argonParallelism, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}
func (p *Passwords) Verify(ctx context.Context, phc, v string) (bool, error) {
	v, valid := normalizedPassword(v)
	if !valid {
		return false, nil
	}
	a := strings.Split(phc, "$")
	if len(a) != 6 || a[1] != "argon2id" || a[2] != "v=19" {
		return false, errors.New("CREDENTIAL_FORMAT_INVALID")
	}
	var m, t uint32
	var parallel uint8
	if n, e := fmt.Sscanf(a[3], "m=%d,t=%d,p=%d", &m, &t, &parallel); e != nil || n != 3 || m < argonMemoryKiB || m > argonMaximumMemoryKiB || t < argonIterations || t > argonMaximumIterations || parallel < argonParallelism || parallel > argonMaximumParallelism || a[3] != fmt.Sprintf("m=%d,t=%d,p=%d", m, t, parallel) {
		return false, errors.New("CREDENTIAL_FORMAT_INVALID")
	}
	salt, e := base64.RawStdEncoding.DecodeString(a[4])
	if e != nil || len(salt) != passwordSaltBytes {
		return false, errors.New("CREDENTIAL_FORMAT_INVALID")
	}
	want, e := base64.RawStdEncoding.DecodeString(a[5])
	if e != nil || len(want) != passwordKeyBytes {
		return false, errors.New("CREDENTIAL_FORMAT_INVALID")
	}
	release, e := p.acquire(ctx)
	if e != nil {
		return false, e
	}
	defer release()
	got := argon2.IDKey([]byte(v), salt, t, m, parallel, passwordKeyBytes)
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
