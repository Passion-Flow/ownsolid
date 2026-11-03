package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"strconv"
	"strings"
	"time"
)

const MinimumHMACKeyBytes = 32
const MaximumHMACKeyBytes = 256

func Token() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic("ENTROPY_UNAVAILABLE")
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func ValidToken(v string) bool {
	if len(v) != 43 {
		return false
	}
	b, e := base64.RawURLEncoding.DecodeString(v)
	return e == nil && len(b) == 32 && base64.RawURLEncoding.EncodeToString(b) == v
}
func Digest(v string) []byte { h := sha256.Sum256([]byte(v)); return h[:] }
func MAC(key []byte, parts ...string) []byte {
	h := hmac.New(sha256.New, key)
	for _, p := range parts {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(p)))
		h.Write(n[:])
		h.Write([]byte(p))
	}
	return h.Sum(nil)
}
func CSRF(key []byte, audience, context, session string, now time.Time, ttl ...time.Duration) (string, time.Time) {
	duration := config.Seconds(config.DefaultRuntimePolicy().Identity.CSRFSeconds)
	if len(ttl) > 0 {
		duration = ttl[0]
	}
	exp := now.Add(duration)
	stamp := strconv.FormatInt(exp.Unix(), 10)
	nonce := Token()
	mac := MAC(key, "m01-csrf", "v1", audience, context, Binding(session), stamp, nonce)
	return "v1." + stamp + "." + nonce + "." + base64.RawURLEncoding.EncodeToString(mac), exp
}
func Binding(session string) string {
	if !ValidToken(session) {
		return "anonymous"
	}
	return base64.RawURLEncoding.EncodeToString(Digest(session))
}
func VerifyCSRF(key []byte, audience, context, session, token string, now time.Time, ttl ...time.Duration) bool {
	duration := config.Seconds(config.DefaultRuntimePolicy().Identity.CSRFSeconds)
	if len(ttl) > 0 {
		duration = ttl[0]
	}
	if !ValidToken(context) || len(token) > 256 {
		return false
	}
	a := strings.Split(token, ".")
	if len(a) != 4 || a[0] != "v1" || !ValidToken(a[2]) {
		return false
	}
	exp, e := strconv.ParseInt(a[1], 10, 64)
	if e != nil || strconv.FormatInt(exp, 10) != a[1] || exp <= now.Unix() || exp > now.Add(duration).Unix() {
		return false
	}
	mac, e := base64.RawURLEncoding.DecodeString(a[3])
	return e == nil && hmac.Equal(mac, MAC(key, "m01-csrf", "v1", audience, context, Binding(session), a[1], a[2]))
}
