package preferences

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

const (
	// MaximumViewsPerList bounds how many views one person keeps on one list.
	MaximumViewsPerList = 50
	// MaximumViewStateBytes bounds a view's state as the page sends it.
	MaximumViewStateBytes = 4096
	maximumViewNameRunes  = 100
)

var listKey = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// SavedView is a named state of one list: its filters, sort and columns (UX-103). The page
// that owns the list owns the meaning of State; the server keeps it as given.
type SavedView struct {
	ID        string          `json:"id"`
	List      string          `json:"list"`
	Name      string          `json:"name"`
	State     json.RawMessage `json:"state"`
	Revision  int64           `json:"revision"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// SavedViewInput creates a view when ExpectedRevision is 0 and replaces it otherwise.
type SavedViewInput struct {
	List             string                     `json:"list"`
	Name             string                     `json:"name"`
	State            map[string]json.RawMessage `json:"state"`
	ExpectedRevision int64                      `json:"expected_revision"`
}

// ValidList reports whether a list key is well formed.
func ValidList(list string) bool { return listKey.MatchString(list) }

func validViewID(id string) bool {
	parsed, e := uuid.Parse(id)
	return e == nil && parsed != uuid.Nil && parsed.String() == id
}

// encodedState is the state as stored, or an error when it is missing or too large.
func (v SavedViewInput) encodedState() (string, error) {
	if v.State == nil {
		return "", failure("VALIDATION_FAILED")
	}
	raw, e := json.Marshal(v.State)
	if e != nil || len(raw) > MaximumViewStateBytes {
		return "", failure("VALIDATION_FAILED")
	}
	return string(raw), nil
}

func (v SavedViewInput) valid() bool {
	name := strings.TrimSpace(v.Name)
	return ValidList(v.List) && name == v.Name && name != "" && utf8.ValidString(name) &&
		utf8.RuneCountInString(name) <= maximumViewNameRunes && !strings.ContainsFunc(name, isControl) &&
		v.ExpectedRevision >= 0 && v.ExpectedRevision < MaximumRevision
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

const savedViewColumns = "id::text,list_key,name,state::text,revision,created_at,updated_at"

func scanView(row transaction.Row) (SavedView, error) {
	var v SavedView
	var state string
	if e := row.Scan(&v.ID, &v.List, &v.Name, &state, &v.Revision, &v.CreatedAt, &v.UpdatedAt); e != nil {
		return SavedView{}, e
	}
	v.State = json.RawMessage(state)
	v.CreatedAt, v.UpdatedAt = v.CreatedAt.UTC(), v.UpdatedAt.UTC()
	return v, nil
}

// postgresOnly refuses chains that have no table for views (CMP-201, CMP-202).
func postgresOnly(tx transaction.Executor) error {
	if tx.Dialect() != transaction.PostgreSQL {
		return failure("PREFERENCE_UNAVAILABLE")
	}
	return nil
}

// ListViews returns the caller's views of one list, by name.
func (s *Service) ListViews(ctx context.Context, a identity.Auth, list string) ([]SavedView, error) {
	if !ValidList(list) {
		return nil, failure("VALIDATION_FAILED")
	}
	out := []SavedView{}
	e := s.transaction(ctx, a, func(ctx context.Context, tx transaction.Executor, _ string) error {
		if e := postgresOnly(tx); e != nil {
			return e
		}
		rows, e := tx.Query(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT "+savedViewColumns+" FROM saved_views WHERE user_id=$1 AND list_key=$2 ORDER BY name,id LIMIT $3", a.Me.User.ID, list, MaximumViewsPerList+1))
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			v, e := scanView(rows)
			if e != nil {
				return e
			}
			out = append(out, v)
		}
		return rows.Err()
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}

// PutView creates or replaces one of the caller's views. Creating takes ExpectedRevision 0;
// a retry of a create that already landed returns the view it made. A stale revision fails
// with PREFERENCE_REVISION_CONFLICT, and another person's view reads as not found.
func (s *Service) PutView(ctx context.Context, a identity.Auth, id string, input SavedViewInput) (SavedView, error) {
	if !validViewID(id) || !input.valid() {
		return SavedView{}, failure("VALIDATION_FAILED")
	}
	state, e := input.encodedState()
	if e != nil {
		return SavedView{}, e
	}
	var out SavedView
	e = s.transaction(ctx, a, func(ctx context.Context, tx transaction.Executor, _ string) error {
		if e := postgresOnly(tx); e != nil {
			return e
		}
		var e error
		if input.ExpectedRevision == 0 {
			out, e = createView(ctx, tx, a.Me.User.ID, id, input, state)
		} else {
			out, e = replaceView(ctx, tx, a.Me.User.ID, id, input, state)
		}
		return e
	})
	if e != nil {
		return SavedView{}, e
	}
	return out, nil
}

// The caller's user row is locked by the session recheck, so counting and inserting cannot
// race another request of the same person.
func createView(ctx context.Context, tx transaction.Executor, user, id string, input SavedViewInput, state string) (SavedView, error) {
	existing, e := scanView(tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT "+savedViewColumns+" FROM saved_views WHERE id=$1 AND user_id=$2", id, user)))
	if e == nil {
		if existing.List == input.List && existing.Name == input.Name && existing.Revision == 1 && equalJSON(existing.State, json.RawMessage(state)) {
			return existing, nil
		}
		return SavedView{}, failure("PREFERENCE_REVISION_CONFLICT")
	}
	if !noRows(e) {
		return SavedView{}, e
	}
	var count int
	if e = tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT count(*) FROM saved_views WHERE user_id=$1 AND list_key=$2", user, input.List)).Scan(&count); e != nil {
		return SavedView{}, e
	}
	if count >= MaximumViewsPerList {
		return SavedView{}, failure("RESOURCE_LIMIT_REACHED")
	}
	if e = nameTaken(ctx, tx, user, input.List, input.Name, id); e != nil {
		return SavedView{}, e
	}
	// ON CONFLICT keeps the transaction usable when the ID belongs to someone else.
	created, e := scanView(tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "INSERT INTO saved_views(id,user_id,list_key,name,state) VALUES($1,$2,$3,$4,$5::jsonb) ON CONFLICT DO NOTHING RETURNING "+savedViewColumns, id, user, input.List, input.Name, state)))
	if noRows(e) {
		return SavedView{}, failure("PREFERENCE_REVISION_CONFLICT")
	}
	return created, e
}

func replaceView(ctx context.Context, tx transaction.Executor, user, id string, input SavedViewInput, state string) (SavedView, error) {
	var list string
	var revision int64
	e := tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT list_key,revision FROM saved_views WHERE id=$1 AND user_id=$2 FOR UPDATE", id, user)).Scan(&list, &revision)
	if noRows(e) {
		return SavedView{}, failure("RESOURCE_NOT_FOUND")
	}
	if e != nil {
		return SavedView{}, e
	}
	if list != input.List {
		return SavedView{}, failure("VALIDATION_FAILED")
	}
	if revision != input.ExpectedRevision {
		return SavedView{}, failure("PREFERENCE_REVISION_CONFLICT")
	}
	if e = nameTaken(ctx, tx, user, input.List, input.Name, id); e != nil {
		return SavedView{}, e
	}
	return scanView(tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "UPDATE saved_views SET name=$1,state=$2::jsonb,revision=revision+1,updated_at=clock_timestamp() WHERE id=$3 AND user_id=$4 RETURNING "+savedViewColumns, input.Name, state, id, user)))
}

func nameTaken(ctx context.Context, tx transaction.Executor, user, list, name, id string) error {
	var taken bool
	if e := tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT EXISTS(SELECT 1 FROM saved_views WHERE user_id=$1 AND list_key=$2 AND name=$3 AND id<>$4)", user, list, name, id)).Scan(&taken); e != nil {
		return e
	}
	if taken {
		return failure("SAVED_VIEW_NAME_EXISTS")
	}
	return nil
}

// DeleteView removes one of the caller's views at the given revision.
func (s *Service) DeleteView(ctx context.Context, a identity.Auth, id string, revision int64) error {
	if !validViewID(id) || revision < 1 || revision >= MaximumRevision {
		return failure("VALIDATION_FAILED")
	}
	return s.transaction(ctx, a, func(ctx context.Context, tx transaction.Executor, _ string) error {
		if e := postgresOnly(tx); e != nil {
			return e
		}
		var current int64
		e := tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT revision FROM saved_views WHERE id=$1 AND user_id=$2 FOR UPDATE", id, a.Me.User.ID)).Scan(&current)
		if noRows(e) {
			return failure("RESOURCE_NOT_FOUND")
		}
		if e != nil {
			return e
		}
		if current != revision {
			return failure("PREFERENCE_REVISION_CONFLICT")
		}
		_, e = tx.Exec(ctx, transaction.SQL(transaction.PostgreSQL, "DELETE FROM saved_views WHERE id=$1 AND user_id=$2", id, a.Me.User.ID))
		return e
	})
}

// equalJSON compares two JSON values by meaning, as PostgreSQL's jsonb normalizes them.
func equalJSON(a, b json.RawMessage) bool {
	var left, right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	l, e1 := json.Marshal(left)
	r, e2 := json.Marshal(right)
	return e1 == nil && e2 == nil && bytes.Equal(l, r)
}
