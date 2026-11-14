package cloudinventory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type Service struct {
	slots      chan struct{}
	Identity   *identity.Service
	Workspaces *workspaces.Service
	Provider   *Provider
}
type ImportInput struct {
	Connection string `json:"connection"`
	Region     string `json:"region"`
	Key        string `json:"idempotency_key"`
}
type Receipt struct {
	Key         string    `json:"idempotency_key"`
	Connection  string    `json:"connection"`
	AccountID   string    `json:"account_id"`
	Region      string    `json:"region"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
	Imported    int       `json:"imported"`
}
type Resource struct {
	ID        string `json:"id"`
	AccountID string `json:"account_id"`
	Region    string `json:"region"`
	Instance
	ObservedAt time.Time `json:"observed_at"`
	Missing    bool      `json:"missing"`
}
type Choice struct {
	Name      string   `json:"name"`
	AccountID string   `json:"account_id"`
	Regions   []string `json:"regions"`
}
type Inventory struct {
	Enabled     bool       `json:"enabled"`
	CanImport   bool       `json:"can_import"`
	Connections []Choice   `json:"connections"`
	Items       []Resource `json:"items"`
	NextCursor  *string    `json:"next_cursor"`
}

func New(i *identity.Service, w *workspaces.Service) *Service {
	return &Service{Identity: i, Workspaces: w, Provider: NewProvider(i.Config.AWS), slots: make(chan struct{}, i.Config.AWS.MaximumConcurrentImports)}
}
func canonical(v string) bool {
	id, e := uuid.Parse(v)
	return e == nil && id != uuid.Nil && id.String() == v
}
func owner(a identity.Auth, w workspaces.Workspace) bool {
	return a.Me.Audience == "admin" && a.Me.User.IsPlatformAdmin || w.Role != nil && *w.Role == "owner"
}
func safe(e error) error {
	if outcome, ok := transaction.CommitOutcome(e); ok {
		if direct, ok := e.(*transaction.Error); ok && direct.Kind == outcome {
			return direct
		}
		return &transaction.Error{Kind: outcome}
	}
	var failure *transaction.Error
	if errors.As(e, &failure) && failure.Kind != transaction.CommitUnknown {
		return fail("STORAGE_UNAVAILABLE")
	}
	return e
}

// Trusted composition must supply the catalog/role-checked selected runtime.
// This module borrows that owner; it never opens/migrates/grants another pool.
func (s *Service) repository() (transaction.Store, error) {
	if s == nil {
		return nil, fail("STORAGE_UNAVAILABLE")
	}
	store, e := s.Identity.Repository()
	if e != nil {
		return nil, e
	}
	switch store.Dialect() {
	case transaction.PostgreSQL, transaction.MySQL, transaction.Dameng:
	default:
		return nil, fail("AWS_BUSINESS_DATABASE_UNSUPPORTED")
	}
	if s.Workspaces == nil || s.Workspaces.Identity != s.Identity || s.Workspaces.DB != nil && s.Workspaces.DB != s.Identity.DB {
		return nil, fail("STORAGE_UNAVAILABLE")
	}
	return store, nil
}
func (s *Service) scope(ctx context.Context, a identity.Auth, workspace string, write bool, fn func(context.Context, transaction.Executor, workspaces.Workspace) error) error {
	store, e := s.repository()
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.Identity.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if q == nil || q.Dialect() != store.Dialect() {
			return fail("STORAGE_UNAVAILABLE")
		}
		if _, pool := q.(transaction.Store); pool {
			return fail("STORAGE_UNAVAILABLE")
		}
		var w workspaces.Workspace
		var e error
		if e = s.Identity.RecheckOrderedWithin(ctx, q, a); e != nil {
			return e
		}
		if write {
			w, e = s.Workspaces.RecheckScopeWithin(ctx, q, a, workspace)
		} else {
			w, e = s.Workspaces.ReadScopeWithin(ctx, q, a, workspace)
		}
		if e != nil {
			return e
		}
		if w.Status != "active" {
			return fail("RESOURCE_NOT_FOUND")
		}
		if write && !owner(a, w) {
			return fail("ACCESS_DENIED")
		}
		return fn(ctx, q, w)
	})
	return safe(e)
}
func (s *Service) connection(workspace, name, region string) (config.AWSConnection, error) {
	p := s.Identity.Config.AWS
	if !p.Enabled {
		return config.AWSConnection{}, fail("AWS_DISABLED")
	}
	for _, a := range p.Connections {
		if a.Name == name && slices.Contains(a.WorkspaceIDs, workspace) && slices.Contains(a.Regions, region) {
			return a, nil
		}
	}
	return config.AWSConnection{}, fail("AWS_SCOPE_REJECTED")
}
func readReceipt(ctx context.Context, q transaction.Executor, w workspaces.Workspace, actor, key string) (Receipt, error) {
	var r Receipt
	var createdBy string
	e := q.QueryRow(ctx, awsSQL(q, awsReceipt, w.TenantID, w.ID, key)).Scan(&r.Key, &r.Connection, &r.AccountID, &r.Region, &r.StartedAt, &r.CompletedAt, &r.Imported, &createdBy)
	if e != nil {
		return Receipt{}, e
	}
	if createdBy != actor {
		return Receipt{}, fail("IDEMPOTENCY_CONFLICT")
	}
	r.StartedAt = r.StartedAt.UTC()
	r.CompletedAt = r.CompletedAt.UTC()
	return r, nil
}
func (s *Service) Receipt(ctx context.Context, a identity.Auth, workspace, key string) (Receipt, error) {
	if !canonical(key) {
		return Receipt{}, fail("VALIDATION_FAILED")
	}
	var out Receipt
	e := s.scope(ctx, a, workspace, false, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		var e error
		out, e = readReceipt(ctx, q, w, a.Me.User.ID, key)
		if errors.Is(e, sql.ErrNoRows) {
			return fail("RESOURCE_NOT_FOUND")
		}
		return e
	})
	if errors.Is(e, sql.ErrNoRows) {
		e = fail("RESOURCE_NOT_FOUND")
	}
	if e != nil {
		return Receipt{}, e
	}
	return out, nil
}
func (s *Service) Import(ctx context.Context, a identity.Auth, workspace string, input ImportInput, request string) (Receipt, error) {
	if !canonical(input.Key) || !canonical(request) {
		return Receipt{}, fail("VALIDATION_FAILED")
	}
	// Validate the selected store and same Identity/Workspace owner before admission.
	_, e := s.repository()
	if e != nil {
		return Receipt{}, e
	}
	if e := s.Identity.RequireNewBusiness(ctx); e != nil {
		return Receipt{}, e
	}
	var out Receipt
	var connection config.AWSConnection
	replay := false
	e = s.scope(ctx, a, workspace, true, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		old, e := readReceipt(ctx, q, w, a.Me.User.ID, input.Key)
		if e == nil {
			if old.Connection != input.Connection || old.Region != input.Region {
				return fail("IDEMPOTENCY_CONFLICT")
			}
			out = old
			replay = true
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		connection, e = s.connection(w.ID, input.Connection, input.Region)
		return e
	})
	if e != nil {
		return Receipt{}, e
	}
	if replay {
		return out, nil
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		return Receipt{}, fail("AWS_CANCELLED")
	default:
		return Receipt{}, fail("AWS_BUSY")
	}
	started := time.Now().UTC().Truncate(time.Microsecond)
	instances, e := s.Provider.Discover(ctx, connection, input.Region)
	if e != nil {
		return Receipt{}, e
	}
	if e = s.Identity.RequireNewBusiness(ctx); e != nil {
		return Receipt{}, e
	}
	e = s.scope(ctx, a, workspace, true, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		old, e := readReceipt(ctx, q, w, a.Me.User.ID, input.Key)
		if e == nil {
			if old.Connection != input.Connection || old.Region != input.Region {
				return fail("IDEMPOTENCY_CONFLICT")
			}
			out = old
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		current, e := s.connection(w.ID, input.Connection, input.Region)
		if e != nil {
			return e
		}
		if current.AccountID != connection.AccountID || current.Partition != connection.Partition {
			return fail("AWS_SCOPE_REJECTED")
		}
		var newer int
		if e = q.QueryRow(ctx, awsSQL(q, awsNewerReceipts, w.TenantID, w.ID, connection.AccountID, input.Region, started)).Scan(&newer); e != nil {
			return e
		}
		if newer > 0 {
			return fail("AWS_SYNC_SUPERSEDED")
		}
		var receipts int
		if e = q.QueryRow(ctx, awsSQL(q, awsReceiptCount, w.TenantID, w.ID)).Scan(&receipts); e != nil {
			return e
		}
		if receipts >= s.Identity.Config.AWS.MaximumReceipts {
			return fail("AWS_LIMIT_REACHED")
		}
		if _, e = q.Exec(ctx, awsSQL(q, awsMarkMissing, w.TenantID, w.ID, connection.AccountID, input.Region)); e != nil {
			return e
		}
		for _, v := range instances {
			raw, e := json.Marshal(v)
			if e != nil {
				return e
			}
			e = upsertResource(ctx, q, uuid.NewString(), w.TenantID, w.ID, connection.AccountID, input.Region, v, string(raw), started)
			if e != nil {
				return e
			}
		}
		var count int
		if e = q.QueryRow(ctx, awsSQL(q, awsResourceCount, w.TenantID, w.ID)).Scan(&count); e != nil {
			return e
		}
		if count > s.Identity.Config.AWS.MaximumResources {
			return fail("AWS_LIMIT_REACHED")
		}
		out = Receipt{input.Key, input.Connection, connection.AccountID, input.Region, started, time.Now().UTC().Truncate(time.Microsecond), len(instances)}
		if _, e = q.Exec(ctx, awsSQL(q, awsInsertReceipt, w.TenantID, w.ID, input.Key, a.Me.User.ID, input.Connection, connection.AccountID, input.Region, out.StartedAt, out.CompletedAt, out.Imported)); e != nil {
			return e
		}
		_, e = q.Exec(ctx, awsSQL(q, awsAuditImport, uuid.NewString(), a.Me.User.ID, w.TenantID, w.ID, a.Me.Audience, input.Key, request))
		return e
	})
	if e != nil {
		return Receipt{}, e
	}
	return out, nil
}
func (s *Service) List(ctx context.Context, a identity.Auth, workspace, after string, limit int) (Inventory, error) {
	if after != "" && !canonical(after) || limit < 1 || limit > s.Identity.Config.Policy().UI.PageSize {
		return Inventory{}, fail("VALIDATION_FAILED")
	}
	out := Inventory{Enabled: s.Identity.Config.AWS.Enabled, Connections: []Choice{}, Items: []Resource{}}
	e := s.scope(ctx, a, workspace, false, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		out.CanImport = owner(a, w) && out.Enabled
		if out.CanImport {
			for _, c := range s.Identity.Config.AWS.Connections {
				if slices.Contains(c.WorkspaceIDs, w.ID) {
					out.Connections = append(out.Connections, Choice{c.Name, c.AccountID, append([]string{}, c.Regions...)})
				}
			}
		}
		lower := after
		if lower == "" {
			lower = uuid.Nil.String()
		}
		rows, e := q.Query(ctx, awsSQL(q, awsListResources, w.TenantID, w.ID, lower, limit+1))
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var r Resource
			var raw []byte
			if e = rows.Scan(&r.ID, &r.AccountID, &r.Region, &raw, &r.ObservedAt, &r.Missing); e != nil {
				return e
			}
			if json.Unmarshal(raw, &r.Instance) != nil {
				return fail("STORAGE_UNAVAILABLE")
			}
			r.ObservedAt = r.ObservedAt.UTC()
			out.Items = append(out.Items, r)
		}
		if e = rows.Err(); e != nil {
			return e
		}
		if len(out.Items) > limit {
			out.Items = out.Items[:limit]
			v := out.Items[limit-1].ID
			out.NextCursor = &v
		}
		return nil
	})
	if e != nil {
		return Inventory{}, e
	}
	return out, nil
}
