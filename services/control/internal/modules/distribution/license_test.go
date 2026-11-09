package distribution_test

import (
	"bytes"
	"context"
	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestM19ActualLicenseAdmissionSingleConnectionAndInflightOutcome(t *testing.T) {
	c := setup(t)
	access := c.access(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	poolConfig := c.s.DB.Config().Copy()
	poolConfig.MaxConns = 1
	one, e := pgxpool.NewWithConfig(ctx, poolConfig)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(one.Close)
	c.s.DB = one
	c.s.Identity.DB = one
	// Keep every same-identity owner on the actual single fixture pool.
	c.s.Workspaces.DB = one
	c.s.Nodes.DB = one
	c.s.Deployments.Models.DB = one
	active, e := licensing.FromConfig(c.s.Identity)
	if e != nil {
		t.Fatal(e)
	}
	c.s.Identity.BusinessAdmission = active
	input := access.input()
	if _, e = c.s.Admit(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, input, uuid.NewString()); e != nil {
		t.Fatal("actual active single connection admission", e)
	}
	trust, e := c.s.Identity.Config.LicenseTrust()
	if e != nil {
		t.Fatal(e)
	}
	expired, e := licensing.New(c.s.Identity, trust, c.s.Identity.Config.LicensePolicy(), func() time.Time { return time.Now().UTC().Add(26 * time.Hour) })
	if e != nil {
		t.Fatal(e)
	}
	c.s.Identity.BusinessAdmission = expired
	denied := access.input()
	_, e = c.s.Admit(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, denied, uuid.NewString())
	check(t, e, "LICENSE_NEW_BUSINESS_UNAVAILABLE")
	var count int
	if e = one.QueryRow(ctx, "SELECT count(*) FROM gateway_requests WHERE id=$1", denied.RequestID).Scan(&count); e != nil || count != 0 {
		t.Fatal("denied admission persisted/held capacity", count, e)
	}
	if _, e = c.s.AuthorizeModels(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, *access.key.Secret); e != nil {
		t.Fatal("readonly discovery unavailable at expiry", e)
	}
	if _, e = c.s.Report(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, success(input.RequestID), uuid.NewString()); e != nil {
		t.Fatal("existing inflight outcome unavailable at expiry", e)
	}
	c.s.Identity.BusinessAdmission = nil
	_, e = c.s.Admit(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, access.input(), uuid.NewString())
	check(t, e, "LICENSE_CONFIGURATION_REQUIRED")
	// Invalid machine authority is refused before a private license observation.
	_, e = c.s.Admit(ctx, access.gateway.Gateway.ID, "invalid", access.input(), uuid.NewString())
	check(t, e, "GATEWAY_UNAUTHORIZED")
}

// License time is explicitly injected; all keys, authorization, locks, capacity,
// request/outcome rows and cleanup are the actual PostgreSQL implementation.
func expiredLicense(t *testing.T, c scenario) *licensing.Service {
	t.Helper()
	trust, e := c.s.Identity.Config.LicenseTrust()
	if e != nil {
		t.Fatal(e)
	}
	service, e := licensing.New(c.s.Identity, trust, c.s.Identity.Config.LicensePolicy(), func() time.Time { return time.Now().UTC().Add(26 * time.Hour) })
	if e != nil {
		t.Fatal(e)
	}
	return service
}

func TestM19AcceptedOperationAcrossExpirySinglePoolAndConcurrentReplay(t *testing.T) {
	c := setup(t)
	access := c.access(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cfg := c.s.DB.Config().Copy()
	cfg.MaxConns = 1
	one, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(one.Close)
	c.s.DB = one
	c.s.Identity.DB = one
	c.s.Workspaces.DB = one
	c.s.Nodes.DB = one
	c.s.Deployments.Models.DB = one
	expired := expiredLicense(t, c)
	var rawKey, keyID string
	e = c.s.WithTransientKey(ctx, access.owner, keyInput(access.alias), 2*time.Second, uuid.NewString(), func(raw string) error {
		rawKey = raw
		parts := strings.Split(raw, ".")
		keyID = parts[1]
		var stored []byte
		if e := one.QueryRow(ctx, "SELECT secret_hash FROM api_keys WHERE id=$1", keyID).Scan(&stored); e != nil {
			t.Fatal(e)
		}
		expected := security.MAC(c.s.Identity.HMACKey, "m07-accepted-operation-key", "v1", keyID, parts[2])
		ordinary := security.MAC(c.s.Identity.HMACKey, "m07-api-key", "v1", keyID, parts[2])
		if !bytes.Equal(stored, expected) || bytes.Equal(stored, ordinary) {
			t.Fatal("private purpose not authenticated")
		}
		c.s.Identity.BusinessAdmission = expired
		_, e := c.s.Admit(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, access.input(), uuid.NewString())
		check(t, e, "LICENSE_NEW_BUSINESS_UNAVAILABLE")
		type result struct {
			input wire.AdmissionRequest
			err   error
		}
		ready := make(chan struct{})
		results := make(chan result, 2)
		var joined sync.WaitGroup
		for range 2 {
			joined.Add(1)
			go func() {
				defer joined.Done()
				input := access.input()
				input.APIKey = raw
				<-ready
				_, e := c.s.Admit(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, input, uuid.NewString())
				results <- result{input, e}
			}()
		}
		close(ready)
		joined.Wait()
		close(results)
		var accepted string
		successes, refusals := 0, 0
		for result := range results {
			if result.err == nil {
				successes++
				accepted = result.input.RequestID
			} else {
				check(t, result.err, "API_KEY_INVALID")
				refusals++
			}
		}
		if successes != 1 || refusals != 1 {
			t.Fatal("private concurrent operation not single use", successes, refusals)
		}
		var count int
		var used bool
		if e = one.QueryRow(ctx, "SELECT count(*) FROM gateway_requests WHERE api_key_id=$1", keyID).Scan(&count); e != nil || count != 1 {
			t.Fatal("extra durable capacity or request", e, count)
		}
		if e = one.QueryRow(ctx, "SELECT last_used_at IS NOT NULL FROM api_keys WHERE id=$1", keyID).Scan(&used); e != nil || !used {
			t.Fatal("single-use consumption not durable", e)
		}
		_, e = c.s.Report(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, success(accepted), uuid.NewString())
		return e
	})
	if e != nil {
		t.Fatal("actual expired accepted operation", e)
	}
	var revoked bool
	if e = one.QueryRow(ctx, "SELECT revoked_at IS NOT NULL FROM api_keys WHERE id=$1", keyID).Scan(&revoked); e != nil || !revoked {
		t.Fatal("private key cleanup", e)
	}
	_, e = c.s.AuthorizeModels(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, rawKey)
	check(t, e, "API_KEY_INVALID")
}

func TestM19QueuedOperationMintAfterExpiryAndMandatoryIntegrity(t *testing.T) {
	for _, kind := range []string{"expired", "nil_configuration", "corrupt_history"} {
		t.Run(kind, func(t *testing.T) {
			c := setup(t)
			access := c.access(t)
			ctx := context.Background()
			c.s.Identity.BusinessAdmission = expiredLicense(t, c)
			var before, after int
			if e := c.s.DB.QueryRow(ctx, "SELECT count(*) FROM api_keys").Scan(&before); e != nil {
				t.Fatal(e)
			}
			want := ""
			switch kind {
			case "nil_configuration":
				c.s.Identity.BusinessAdmission = nil
				want = "LICENSE_CONFIGURATION_REQUIRED"
			case "corrupt_history":
				if _, e := c.s.DB.Exec(ctx, "UPDATE platform_license_artifacts SET artifact_sha256=repeat('0',64)"); e != nil {
					t.Fatal(e)
				}
				want = "LICENSE_STORED_STATE_INVALID"
			}
			calls := 0
			e := c.s.WithTransientKey(ctx, access.owner, keyInput(access.alias), 2*time.Second, uuid.NewString(), func(raw string) error {
				calls++
				input := access.input()
				input.APIKey = raw
				if _, e := c.s.Admit(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, input, uuid.NewString()); e != nil {
					return e
				}
				_, e := c.s.Report(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, success(input.RequestID), uuid.NewString())
				return e
			})
			if want == "" {
				if e != nil || calls != 1 {
					t.Fatal("queued accepted operation after expiry", e, calls)
				}
			} else {
				check(t, e, want)
				if calls != 0 {
					t.Fatal("invalid license state entered callback")
				}
			}
			if e = c.s.DB.QueryRow(ctx, "SELECT count(*) FROM api_keys").Scan(&after); e != nil {
				t.Fatal(e)
			}
			if want != "" && before != after {
				t.Fatal("invalid state minted authority")
			}
			if kind == "expired" {
				input := keyInput(access.alias)
				input.Label = "accepted-operation-key"
				ordinary, e := c.s.MintKey(ctx, access.owner, input, uuid.NewString(), uuid.NewString())
				if e != nil {
					t.Fatal(e)
				}
				request := access.input()
				request.APIKey = *ordinary.Secret
				_, e = c.s.Admit(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, request, uuid.NewString())
				check(t, e, "LICENSE_NEW_BUSINESS_UNAVAILABLE")
			}
		})
	}
}

func TestM19AcceptedOperationRetainsCurrentAuthorityAndCleanup(t *testing.T) {
	for _, kind := range []string{"actor_epoch", "membership", "key", "gateway", "alias", "nil_configuration", "corrupt_history"} {
		t.Run(kind, func(t *testing.T) {
			c := setup(t)
			access := c.access(t)
			ctx := context.Background()
			var keyID string
			e := c.s.WithTransientKey(ctx, access.owner, keyInput(access.alias), 2*time.Second, uuid.NewString(), func(raw string) error {
				keyID = strings.Split(raw, ".")[1]
				c.s.Identity.BusinessAdmission = expiredLicense(t, c)
				var e error
				want := "API_KEY_INVALID"
				switch kind {
				case "actor_epoch":
					_, e = c.s.DB.Exec(ctx, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", access.owner.Me.User.ID)
				case "membership":
					_, e = c.s.DB.Exec(ctx, "UPDATE workspace_memberships SET authority_id=$1 WHERE workspace_id=$2 AND user_id=$3", uuid.NewString(), access.alias.WorkspaceID, access.owner.Me.User.ID)
				case "key":
					_, e = c.s.RevokeKey(ctx, access.owner, access.alias.WorkspaceID, keyID, "Synthetic existing key revocation", uuid.NewString())
				case "gateway":
					_, e = c.s.RevokeGateway(ctx, c.a, access.alias.WorkspaceID, access.gateway.Gateway.ID, "Synthetic existing gateway revocation", uuid.NewString())
					want = "GATEWAY_UNAUTHORIZED"
				case "alias":
					_, e = c.s.DisableAlias(ctx, c.a, access.alias.WorkspaceID, access.alias.ID, access.alias.Revision, "Synthetic existing alias disable", uuid.NewString())
					want = "MODEL_ALIAS_UNAVAILABLE"
				case "nil_configuration":
					c.s.Identity.BusinessAdmission = nil
					want = "LICENSE_CONFIGURATION_REQUIRED"
				case "corrupt_history":
					_, e = c.s.DB.Exec(ctx, "UPDATE platform_license_artifacts SET artifact_sha256=repeat('0',64)")
					want = "LICENSE_STORED_STATE_INVALID"
				}
				if e != nil {
					t.Fatal(e)
				}
				input := access.input()
				input.APIKey = raw
				_, e = c.s.Admit(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, input, uuid.NewString())
				check(t, e, want)
				var count int
				if e = c.s.DB.QueryRow(ctx, "SELECT count(*) FROM gateway_requests WHERE api_key_id=$1", keyID).Scan(&count); e != nil || count != 0 {
					t.Fatal("refused private operation holds capacity", e, count)
				}
				return nil
			})
			if e != nil {
				t.Fatal("independent possession cleanup after lost authority", e)
			}
			var revoked bool
			if e = c.s.DB.QueryRow(ctx, "SELECT revoked_at IS NOT NULL FROM api_keys WHERE id=$1", keyID).Scan(&revoked); e != nil || !revoked {
				t.Fatal("private authority leaked", e)
			}
		})
	}
}
