// Package releases exposes only the configured installation's recorded release history.
package releases

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/localization"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
	"time"
)

type Service struct {
	identity *identity.Service
	config   Config
	locales  *localization.Registry
	// The production constructor always binds the protected filesystem reader.
	reader func(Config) (view.Snapshot, error)
}
type Locale struct {
	Locale                   string `json:"locale"`
	PackageSHA256            string `json:"package_sha256"`
	TranslationState         string `json:"translation_state"`
	GoogleTranslatedComplete bool   `json:"google_translated_complete"`
}
type Current struct {
	Snapshot      view.Snapshot `json:"snapshot"`
	Database      string        `json:"database"`
	AppliedSchema int           `json:"applied_schema"`
	Locales       []Locale      `json:"locales"`
	LiveHealth    bool          `json:"live_health"`
}
type History struct {
	ScopeID       string       `json:"scope_id"`
	HeadSHA256    string       `json:"head_sha256"`
	Sequence      int          `json:"sequence"`
	FirstSequence int          `json:"first_sequence"`
	Items         []view.Event `json:"items"`
	NextCursor    *string      `json:"next_cursor"`
}
type cursor struct {
	Scope  string `json:"scope"`
	Head   string `json:"head"`
	Before int    `json:"before"`
}

func New(i *identity.Service, l *localization.Registry, c Config) (*Service, error) {
	if i == nil || l == nil || c.Validate() != nil {
		return nil, errors.New("RELEASE_VIEW_CONFIGURATION_INVALID")
	}
	if _, e := i.Repository(); e != nil {
		return nil, e
	}
	return &Service{identity: i, config: c, locales: l, reader: readSnapshot}, nil
}
func (s *Service) VerifyStored(ctx context.Context) error {
	if s == nil || s.reader == nil || ctx == nil || ctx.Err() != nil {
		return unavailable()
	}
	_, e := s.reader(s.config)
	return e
}
func (s *Service) within(ctx context.Context, a identity.Auth, requestID, action string, fn func(context.Context, transaction.Executor, view.Snapshot) error) error {
	if s == nil || ctx == nil {
		return unavailable()
	}
	if a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin {
		return errors.New("ACCESS_DENIED")
	}
	if s.identity == nil || s.locales == nil || s.reader == nil || s.config.Validate() != nil {
		return unavailable()
	}
	id, e := uuid.Parse(requestID)
	if e != nil || id == uuid.Nil || id.String() != requestID {
		return errors.New("VALIDATION_FAILED")
	}
	store, e := s.identity.Repository()
	if e != nil {
		return unavailable()
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(s.config.OperationSeconds)*time.Second)
	defer cancel()
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if e := s.identity.RecheckOrderedWithin(ctx, q, a); e != nil {
			return e
		}
		query := "SELECT t.id::text FROM installation_state i JOIN tenants t ON t.id=i.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND t.status='active' FOR SHARE OF i,t"
		if q.Dialect() == transaction.MySQL {
			query = "SELECT t.id FROM installation_state i JOIN tenants t ON t.id=i.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND t.status='active' FOR SHARE"
		}
		var tenant string
		if e := q.QueryRow(ctx, transaction.SQL(q.Dialect(), query)).Scan(&tenant); e != nil {
			return e
		}
		if id, e := uuid.Parse(tenant); e != nil || id == uuid.Nil || id.String() != tenant {
			return unavailable()
		}
		snapshot, e := s.reader(s.config)
		if e != nil {
			return e
		}
		if e = fn(ctx, q, snapshot); e != nil {
			return e
		}
		if e = s.identity.RecheckOrderedWithin(ctx, q, a); e != nil {
			return e
		}
		after, e := s.reader(s.config)
		if e != nil {
			return e
		}
		if after.HeadSHA256 != snapshot.HeadSHA256 {
			return errors.New("RELEASE_VIEW_CHANGED")
		}
		// scope nonce is not a UUID; target_id is intentionally SQL NULL. The bounded
		// canonical scope/head identifies the read in the audit reason instead.
		return audit.WriteWithin(ctx, q, a.Me.User.ID, "", "admin", action, "success", "release_installation", "", "scope="+snapshot.ScopeID+" head="+snapshot.HeadSHA256, requestID)
	})
	if e == nil {
		return nil
	}
	switch e.Error() {
	case "ACCESS_DENIED", "AUTH_SESSION_EXPIRED", "INVALID_CURSOR", "RELEASE_VIEW_CHANGED", "RELEASE_VIEW_UNAVAILABLE":
		return e
	}
	if kind, ok := transaction.CommitOutcome(e); ok {
		return errors.New(string(kind))
	}
	return unavailable()
}
func (s *Service) Current(ctx context.Context, a identity.Auth, requestID string) (Current, error) {
	var out Current
	e := s.within(ctx, a, requestID, "release.current.read", func(ctx context.Context, q transaction.Executor, snapshot view.Snapshot) error {
		query := "SELECT COALESCE(MAX(version),0) FROM schema_migrations"
		if q.Dialect() == transaction.MySQL {
			query = "SELECT COALESCE(MAX(step),0) FROM schema_migrations WHERE state='applied'"
		}
		var version int
		if e := q.QueryRow(ctx, transaction.SQL(q.Dialect(), query)).Scan(&version); e != nil {
			return e
		}
		out = Current{Snapshot: snapshot, Database: string(q.Dialect()), AppliedSchema: version, Locales: []Locale{}, LiveHealth: false}
		for _, l := range s.locales.Catalog() {
			out.Locales = append(out.Locales, Locale{l.Locale, l.PackageSHA256, l.TranslationState, l.GoogleTranslatedComplete})
		}
		return nil
	})
	if e != nil {
		return Current{}, e
	}
	return out, nil
}
func history(s view.Snapshot, after string, size int) (History, error) {
	before := s.Sequence + 1
	if after != "" {
		raw, e := base64.RawURLEncoding.DecodeString(after)
		var c cursor
		if e != nil || len(raw) > 512 || wire.StrictJSON(raw, &c) != nil || c.Scope != s.ScopeID || c.Head != s.HeadSHA256 || c.Before <= s.FirstSequence || c.Before > s.Sequence {
			return History{}, errors.New("RELEASE_VIEW_CHANGED")
		}
		before = c.Before
	}
	out := History{ScopeID: s.ScopeID, HeadSHA256: s.HeadSHA256, Sequence: s.Sequence, FirstSequence: s.FirstSequence, Items: []view.Event{}}
	for i := len(s.Events) - 1; i >= 0; i-- {
		e := s.Events[i]
		if e.Sequence >= before {
			continue
		}
		if len(out.Items) == size {
			raw, _ := json.Marshal(cursor{s.ScopeID, s.HeadSHA256, out.Items[len(out.Items)-1].Sequence})
			next := base64.RawURLEncoding.EncodeToString(raw)
			out.NextCursor = &next
			break
		}
		out.Items = append(out.Items, e)
	}
	return out, nil
}
func (s *Service) History(ctx context.Context, a identity.Auth, after, requestID string) (History, error) {
	var out History
	e := s.within(ctx, a, requestID, "release.history.read", func(_ context.Context, _ transaction.Executor, snapshot view.Snapshot) error {
		var e error
		out, e = history(snapshot, after, s.config.PageSize)
		return e
	})
	if e != nil {
		return History{}, e
	}
	return out, nil
}
