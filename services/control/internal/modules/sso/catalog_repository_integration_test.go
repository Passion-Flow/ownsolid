//go:build repository_integration

package sso

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func catalogRepositoryFixture(t *testing.T) sourceRepositoryFixture {
	t.Helper()
	f := newSourceRepositoryFixture(t)
	f.accounts.HMACKey = make([]byte, 32)
	if _, e := rand.Read(f.accounts.HMACKey); e != nil {
		t.Fatal(e)
	}
	f.accounts.Sources = f.s
	return f
}

func catalogAuth(t *testing.T, f sourceRepositoryFixture, user, audience string, source *identity.SessionSource) identity.Auth {
	t.Helper()
	var issued identity.IssuedSession
	e := f.f.Store.Within(f.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var epoch int64
		if e := q.QueryRow(ctx, f.f.Statement(`SELECT auth_epoch FROM users WHERE id=$1`, `SELECT auth_epoch FROM users WHERE id=?`, user)).Scan(&epoch); e != nil {
			return e
		}
		var e error
		issued, e = f.accounts.IssueSessionWithin(ctx, q, user, epoch, audience, "", uuid.NewString(), source)
		return e
	})
	if e != nil {
		t.Fatal("actual catalog session issuance", e)
	}
	a, e := f.accounts.Authenticate(f.ctx, issued.Token, audience)
	if e != nil {
		t.Fatal("actual catalog authentication", e)
	}
	return a
}

func catalogSeedConnector(t *testing.T, f sourceRepositoryFixture, tenant, origin, name string, enabled bool, stamp time.Time) string {
	t.Helper()
	id := uuid.NewString()
	sealed, e := f.s.keys.seal(secretScope{tenant, id, id, clientSecretPurpose}, []byte(security.Token()))
	if e != nil {
		t.Fatal(e)
	}
	f.exec(t, f.f.Statement(
		`INSERT INTO sso_connectors(id,tenant_id,name,provider,origin,client_id,secret_key_id,secret_nonce,secret_ciphertext,enabled,created_by,created_at,updated_at) VALUES($1,$2,$3,'gitea',$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		`INSERT INTO sso_connectors(id,tenant_id,name,provider,origin,client_id,secret_key_id,secret_nonce,secret_ciphertext,enabled,created_by,created_at,updated_at) VALUES(?,?,?,'gitea',?,?,?,?,?,?,?,?,?)`,
		id, tenant, name, origin, uuid.NewString(), sealed.keyID, sealed.nonce, sealed.ciphertext, enabled, f.f.User, stamp, stamp))
	return id
}

func catalogReadCounts(t *testing.T, f sourceRepositoryFixture) [3]int {
	t.Helper()
	var counts [3]int
	for i, name := range []string{"audit_events", "sso_mutation_intents", "sso_connectors"} {
		if e := f.f.Store.QueryRow(f.ctx, f.f.Statement("SELECT COUNT(*) FROM "+name, "SELECT COUNT(*) FROM "+name)).Scan(&counts[i]); e != nil {
			t.Fatal(e)
		}
	}
	return counts
}

func TestActualSSOCatalogPublicAndPagedIsolation(t *testing.T) {
	f := catalogRepositoryFixture(t)
	a := catalogAuth(t, f, f.f.User, "admin", nil)
	stamp := time.Date(2024, 1, 2, 3, 4, 5, 123456000, time.UTC)
	origin := f.accounts.Config.SSO.Providers[0].Origin
	approved := []string{}
	for _, name := range []string{"Catalog \u65e5\u672c\u8a9e", "Catalog \u0627\u0644\u0639\u0631\u0628\u064a\u0629", "Catalog \u4e16\u754c"} {
		approved = append(approved, catalogSeedConnector(t, f, f.f.Tenant, origin, name, true, stamp))
	}
	disabled := catalogSeedConnector(t, f, f.f.Tenant, origin, "Disabled synthetic connector", false, stamp)
	unapproved := catalogSeedConnector(t, f, f.f.Tenant, "https://unapproved.example.invalid", "Unapproved synthetic connector", true, stamp)
	foreignTenant := uuid.NewString()
	f.exec(t, f.f.Statement(`INSERT INTO tenants(id,name,status) VALUES($1,'Foreign catalog tenant','active')`, `INSERT INTO tenants(id,name,status) VALUES(?,'Foreign catalog tenant','active')`, foreignTenant))
	foreign := catalogSeedConnector(t, f, foreignTenant, origin, "Foreign synthetic connector", true, stamp)
	before := catalogReadCounts(t, f)
	items, e := f.s.PublicConnectors(f.ctx)
	if e != nil || len(items) != len(approved)+1 {
		t.Fatal("actual public approved installation scope", e)
	}
	sort.Strings(approved)
	expectedPublic := append(append([]string{}, approved...), f.source.ConnectorID)
	for i, item := range items {
		if item.ID != expectedPublic[i] || item.ID == foreign || item.ID == disabled || item.ID == unapproved {
			t.Fatal("public scope or deterministic equal-time order")
		}
		raw, _ := json.Marshal(item)
		var fields map[string]any
		if json.Unmarshal(raw, &fields) != nil || len(fields) != 3 || fields["id"] == nil || fields["name"] == nil || fields["provider"] != ProviderGitea {
			t.Fatal("public connector exposed private fields")
		}
	}
	tail := append(append([]string{}, approved...), disabled, unapproved)
	sort.Sort(sort.Reverse(sort.StringSlice(tail)))
	expected := append([]string{f.source.ConnectorID}, tail...)
	observed := []string{}
	cursor := ""
	for range 10 {
		page, e := f.s.ListConnectors(f.ctx, a, 2, cursor)
		if e != nil || len(page.Items) > 2 {
			t.Fatal("actual bounded Admin catalog", e)
		}
		for _, item := range page.Items {
			observed = append(observed, item.ID)
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if strings.Join(observed, ",") != strings.Join(expected, ",") || before != catalogReadCounts(t, f) {
		t.Fatal("catalog missing/duplicate/foreign rows or read persisted a mutation")
	}
	f.s.policy.MaximumConnectors = 1
	if items, e = f.s.PublicConnectors(f.ctx); e != nil || len(items) > 1 {
		t.Fatal("configured public capacity", e)
	}
	f.s.enabled = false
	if items, e = f.s.PublicConnectors(f.ctx); e != nil || len(items) != 0 {
		t.Fatal("disabled public recovery", e)
	}
	f.s.enabled = true
	f.exec(t, f.f.Statement(`UPDATE tenants SET status='disabled' WHERE id=$1`, `UPDATE tenants SET status='disabled' WHERE id=?`, f.f.Tenant))
	if items, e = f.s.PublicConnectors(f.ctx); e != nil || len(items) != 0 {
		t.Fatal("inactive installation tenant exposed public connectors", e)
	}
	page, e := f.s.ListConnectors(f.ctx, a, 2, "")
	if e == nil || e.Error() != "SSO_DISABLED" || len(page.Items) != 0 || page.NextCursor != nil {
		t.Fatal("inactive tenant exposed Admin page", e)
	}
}

func TestActualSSOCatalogCurrentAuthorityAndCursorScope(t *testing.T) {
	f := catalogRepositoryFixture(t)
	a := catalogAuth(t, f, f.f.User, "admin", nil)
	catalogSeedConnector(t, f, f.f.Tenant, f.accounts.Config.SSO.Providers[0].Origin, "Cursor sibling", true, time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC))
	page, e := f.s.ListConnectors(f.ctx, a, 1, "")
	if e != nil || len(page.Items) != 1 || page.NextCursor == nil {
		t.Fatal("actual current actor page", e)
	}
	payload := strings.Split(*page.NextCursor, ".")[0]
	for _, scope := range [][3]string{{uuid.NewString(), a.Me.User.ID, "admin"}, {f.f.Tenant, a.Me.User.ID, "portal"}} {
		cursor := payload + "." + base64.RawURLEncoding.EncodeToString(security.MAC(f.accounts.HMACKey, connectorCursorDomain, scope[0], scope[1], scope[2], payload))
		out, e := f.s.ListConnectors(f.ctx, a, 1, cursor)
		if e == nil || e.Error() != "VALIDATION_FAILED" || len(out.Items) != 0 || out.NextCursor != nil {
			t.Fatal("foreign tenant/audience cursor accepted", e)
		}
	}
	other := uuid.NewString()
	email := "synthetic+" + other + "@example.invalid"
	f.exec(t, f.f.Statement(`INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES($1,$2,$3,'Other catalog Admin','en','active',true)`, `INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES(?,?,?,'Other catalog Admin','en','active',true)`, other, email, email))
	otherAuth := catalogAuth(t, f, other, "admin", nil)
	out, e := f.s.ListConnectors(f.ctx, otherAuth, 1, *page.NextCursor)
	if e == nil || e.Error() != "VALIDATION_FAILED" || len(out.Items) != 0 || out.NextCursor != nil {
		t.Fatal("foreign actor cursor accepted", e)
	}
	f.exec(t, f.f.Statement(`UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='catalog_test' WHERE id=$1`, `UPDATE sessions SET revoked_at=UTC_TIMESTAMP(6),revoke_reason='catalog_test' WHERE id=?`, a.SessionID))
	out, e = f.s.ListConnectors(f.ctx, a, 1, "")
	if e == nil || e.Error() != "AUTH_SESSION_EXPIRED" || len(out.Items) != 0 || out.NextCursor != nil {
		t.Fatal("revoked ordinary session exposed page", e)
	}
	local := catalogAuth(t, f, f.f.User, "admin", nil)
	sourced := catalogAuth(t, f, f.f.User, "admin", &f.source)
	if out, e = f.s.ListConnectors(f.ctx, sourced, 1, ""); e != nil || len(out.Items) != 1 {
		t.Fatal("actual live sourced Admin page", e)
	}
	f.exec(t, f.f.Statement(`UPDATE sso_bindings SET active=false,revision=revision+1 WHERE id=$1`, `UPDATE sso_bindings SET active=false,revision=revision+1 WHERE id=?`, f.source.BindingID))
	out, e = f.s.ListConnectors(f.ctx, sourced, 1, "")
	if e == nil || e.Error() != "AUTH_SESSION_EXPIRED" || len(out.Items) != 0 || out.NextCursor != nil {
		t.Fatal("revoked source exposed page", e)
	}
	f.exec(t, f.f.Statement(`UPDATE users SET status='disabled',auth_epoch=auth_epoch+1 WHERE id=$1`, `UPDATE users SET status='disabled',auth_epoch=auth_epoch+1 WHERE id=?`, f.f.User))
	out, e = f.s.ListConnectors(f.ctx, local, 1, "")
	if e == nil || e.Error() != "AUTH_SESSION_EXPIRED" || len(out.Items) != 0 || out.NextCursor != nil {
		t.Fatal("disabled actor exposed page", e)
	}
}

// This test-only observer forwards every operation to the real store. It marks
// an actual query attempt;it supplies no SQL rows,commit or authorization result.
type catalogObservedStore struct {
	transaction.Store
	at    int
	ready chan struct{}
}
type catalogObservedExecutor struct {
	transaction.Executor
	owner *catalogObservedStore
	rows  int
}

func (s *catalogObservedStore) Within(ctx context.Context, readOnly bool, fn func(context.Context, transaction.Executor) error) error {
	return s.Store.Within(ctx, readOnly, func(ctx context.Context, q transaction.Executor) error {
		return fn(ctx, &catalogObservedExecutor{Executor: q, owner: s})
	})
}
func (q *catalogObservedExecutor) QueryRow(ctx context.Context, statement transaction.Statement) transaction.Row {
	q.rows++
	if q.rows == q.owner.at {
		select {
		case q.owner.ready <- struct{}{}:
		default:
		}
	}
	return q.Executor.QueryRow(ctx, statement)
}

func TestActualSSOCatalogWaitingCancellationAndRecovery(t *testing.T) {
	for _, target := range []string{"user", "tenant", "cancel"} {
		t.Run(target, func(t *testing.T) {
			f := catalogRepositoryFixture(t)
			a := catalogAuth(t, f, f.f.User, "admin", nil)
			ready, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unlock := func() { once.Do(func() { close(release) }) }
			defer unlock()
			holderDone := make(chan error, 1)
			holderContext, stopHolder := context.WithTimeout(f.ctx, 5*time.Second)
			defer stopHolder()
			go func() {
				holderDone <- f.f.Store.Within(holderContext, false, func(ctx context.Context, q transaction.Executor) error {
					var id string
					query := f.f.Statement(`SELECT id::text FROM users WHERE id=$1 FOR UPDATE`, `SELECT id FROM users WHERE id=? FOR UPDATE`, f.f.User)
					if target == "tenant" {
						query = f.f.Statement(`SELECT id::text FROM tenants WHERE id=$1 FOR UPDATE`, `SELECT id FROM tenants WHERE id=? FOR UPDATE`, f.f.Tenant)
					}
					if e := q.QueryRow(ctx, query).Scan(&id); e != nil {
						return e
					}
					close(ready)
					select {
					case <-release:
					case <-ctx.Done():
						return ctx.Err()
					}
					if target == "cancel" {
						return nil
					}
					update := f.f.Statement(`UPDATE users SET status='disabled',auth_epoch=auth_epoch+1 WHERE id=$1`, `UPDATE users SET status='disabled',auth_epoch=auth_epoch+1 WHERE id=?`, f.f.User)
					if target == "tenant" {
						update = f.f.Statement(`UPDATE tenants SET status='disabled' WHERE id=$1`, `UPDATE tenants SET status='disabled' WHERE id=?`, f.f.Tenant)
					}
					_, e := q.Exec(ctx, update)
					return e
				})
			}()
			select {
			case <-ready:
			case e := <-holderDone:
				t.Fatal("actual holder failed", e)
			case <-time.After(2 * time.Second):
				t.Fatal("actual holder deadline")
			}
			observed := &catalogObservedStore{Store: f.f.Store, at: 1, ready: make(chan struct{}, 1)}
			if target == "tenant" {
				observed.at = 8 // actor,plain source,peek,user/session/source recheck,installation,tenant
			}
			originalStore, originalDB := f.accounts.Storage, f.accounts.DB
			f.accounts.Storage, f.accounts.DB = observed, nil
			defer func() { f.accounts.Storage, f.accounts.DB = originalStore, originalDB }()
			readerContext, stopReader := context.WithCancel(f.ctx)
			defer stopReader()
			type outcome struct {
				page ConnectorPage
				err  error
			}
			readerDone := make(chan outcome, 1)
			go func() { page, e := f.s.ListConnectors(readerContext, a, 1, ""); readerDone <- outcome{page, e} }()
			select {
			case <-observed.ready:
			case result := <-readerDone:
				t.Fatal("reader completed before owned lock attempt", result.err)
			case <-time.After(2 * time.Second):
				t.Fatal("reader attempt deadline")
			}
			select {
			case result := <-readerDone:
				t.Fatal("reader bypassed held real lock", result.err)
			case <-time.After(50 * time.Millisecond):
			}
			if target == "cancel" {
				stopReader()
			} else {
				unlock()
			}
			var result outcome
			select {
			case result = <-readerDone:
			case <-time.After(2 * time.Second):
				t.Fatal("bounded reader recovery deadline")
			}
			if len(result.page.Items) != 0 || result.page.NextCursor != nil || result.err == nil {
				t.Fatal("waiting failure exposed partial page")
			}
			if target == "cancel" {
				if !errors.Is(result.err, context.Canceled) {
					t.Fatal("actual cancellation classification", result.err)
				}
				unlock()
			} else {
				expected := "AUTH_SESSION_EXPIRED"
				if target == "tenant" {
					expected = "SSO_DISABLED"
				}
				if result.err.Error() != expected {
					t.Fatal("waiting read did not recheck committed authority", result.err)
				}
			}
			select {
			case e := <-holderDone:
				if e != nil {
					t.Fatal("actual holder commit", e)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("holder cleanup deadline")
			}
			if target == "cancel" {
				page, e := f.s.ListConnectors(f.ctx, a, 1, "")
				if e != nil || len(page.Items) != 1 {
					t.Fatal("cancelled reader did not recover after actual lock release", e)
				}
			}
		})
	}
}
