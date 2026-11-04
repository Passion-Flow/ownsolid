package security

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/argon2"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func TestM01ActualHashAdmissionDeadline(t *testing.T) {
	policy := config.DefaultRuntimePolicy()
	// The synthetic hash must outlast the window: about 800 ms on an M4 Max against the 2000 ms default.
	policy.Identity.HashAdmissionMilliseconds = 200
	dir := t.TempDir()
	if os.Chmod(dir, 0700) != nil {
		t.Fatal("HASH_ADMISSION_PRIVATE_FIXTURE_FAILED")
	}
	blocklist := filepath.Join(dir, "blocklist")
	if os.WriteFile(blocklist, []byte("passwordpassword\n"), 0600) != nil {
		t.Fatal("HASH_ADMISSION_BLOCKLIST_FIXTURE_FAILED")
	}
	p, e := NewPasswords(blocklist, policy.Identity)
	if e != nil {
		t.Fatal("HASH_ADMISSION_ACTUAL_CONSTRUCTOR_FAILED", e)
	}
	if cap(p.slots) != 2 || cap(p.queue) != 18 {
		t.Fatal("HASH_ADMISSION_CANONICAL_DEFAULT_CHANGED")
	}
	// Compute a valid supported slow PHC; no channel is filled by a test token.
	salt := make([]byte, 16)
	if _, e := rand.Read(salt); e != nil {
		t.Fatal("HASH_ADMISSION_SYNTHETIC_SALT_FAILED")
	}
	digest := argon2.IDKey([]byte("Synthetic admission credential"), salt, 10, 65536, 1, 32)
	phc := "$argon2id$v=19$m=65536,t=10,p=1$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(digest)
	ctx, cancel := context.WithTimeout(t.Context(), config.Seconds(policy.HTTP.RequestSeconds))
	defer cancel()
	type result struct {
		matched bool
		err     error
	}
	results := make(chan result, 2)
	ready := make(chan struct{})
	var workers sync.WaitGroup
	t.Cleanup(func() { cancel(); workers.Wait() })
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-ready
			ok, e := p.Verify(ctx, phc, "Synthetic incorrect admission credential")
			results <- result{ok, e}
		}()
	}
	close(ready)
	until := time.Now().Add(10 * time.Second)
	for len(p.slots) != 2 && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if len(p.slots) != 2 || len(p.queue) != 2 {
		t.Fatal("HASH_ADMISSION_TWO_REAL_WORKERS_NOT_OBSERVED")
	}
	// Only these two real verifications can use this private Passwords instance.
	// Queue occupancy is2of18, and the independent waiter context stays live.
	// AUTH_CAPACITY_EXCEEDED therefore identifies its actual admission timer,
	// rather than queue overflow, TLS/SQL latency or context cancellation.
	waitContext, waitCancel := context.WithTimeout(t.Context(), config.Seconds(policy.HTTP.RequestSeconds))
	defer waitCancel()
	started := time.Now()
	matched, waitError := p.Verify(waitContext, phc, "Synthetic third admission credential")
	elapsed := time.Since(started)
	if matched || waitError == nil || waitError.Error() != "AUTH_CAPACITY_EXCEEDED" || waitContext.Err() != nil || len(p.slots) != 2 || len(p.queue) != 2 || elapsed < config.Milliseconds(policy.Identity.HashAdmissionMilliseconds) || elapsed > config.Seconds(policy.HTTP.RequestSeconds) {
		t.Fatal("HASH_ADMISSION_REAL_TIMER_BRANCH_NOT_PROVEN", elapsed.Milliseconds())
	}
	for range 2 {
		select {
		case result := <-results:
			if result.matched || result.err != nil {
				t.Fatal("HASH_ADMISSION_REAL_WORKER_RESULT_INVALID")
			}
		case <-ctx.Done():
			t.Fatal("HASH_ADMISSION_WORKER_JOIN_DEADLINE")
		}
	}
	workers.Wait()
	if len(p.slots) != 0 || len(p.queue) != 0 {
		t.Fatal("HASH_ADMISSION_OCCUPANCY_NOT_RELEASED")
	}
	credential, e := p.Hash(t.Context(), "Synthetic recovered admission credential")
	if e != nil {
		t.Fatal("HASH_ADMISSION_RECOVERY_HASH_FAILED")
	}
	if ok, e := p.Verify(t.Context(), credential, "Synthetic recovered admission credential"); e != nil || !ok {
		t.Fatal("HASH_ADMISSION_RECOVERY_VERIFY_FAILED")
	}
	t.Logf("ACTUAL_HASH_ADMISSION timer_ms=%d actual_wait_ms=%d actual_workers=2 queue_before=2 queue_capacity=18 context_live=true overflow_excluded=true timer_branch_attributed=true all_workers_joined=true slots_after=0 queue_after=0 recovery_verified=true", policy.Identity.HashAdmissionMilliseconds, elapsed.Milliseconds())
}
