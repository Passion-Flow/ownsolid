package productagent

import (
	"context"
	"errors"
	"maps"
	"net/url"
	"slices"

	"github.com/google/uuid"

	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/applications"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

var (
	// ErrInvalidCommand means the text or cursor does not meet the command contract.
	ErrInvalidCommand = errors.New("PRODUCT_AGENT_COMMAND_INVALID")
	// ErrUnknownCommand means the text did not match a registered alias.
	ErrUnknownCommand = errors.New("PRODUCT_AGENT_COMMAND_UNKNOWN")
	// ErrCommandDenied means the server policy does not admit the command.
	ErrCommandDenied = errors.New("ACCESS_DENIED")
	// ErrWorkspaceRequired means the command has no authenticated workspace.
	ErrWorkspaceRequired = errors.New("PRODUCT_AGENT_WORKSPACE_REQUIRED")
	// ErrInvalidRequestID means the supplied server correlation ID is invalid.
	ErrInvalidRequestID = errors.New("PRODUCT_AGENT_REQUEST_ID_INVALID")
	// ErrUnsupportedCommandLocale means no command phrases are bundled for that locale.
	ErrUnsupportedCommandLocale = errors.New("PRODUCT_AGENT_COMMAND_LOCALE_UNSUPPORTED")
)

// CommandHelp describes the phrases a caller can enter for a command.
type CommandHelp struct {
	ID      string   `json:"id"`
	Aliases []string `json:"aliases"`
}

// Result holds exactly one navigation or read result. Paths belong to the current entrance.
type Result struct {
	Command       string                                        `json:"command"`
	Path          string                                        `json:"path,omitempty"`
	Applications  *applications.Page[applications.Application]  `json:"applications,omitempty"`
	Conversations *applications.Page[applications.Conversation] `json:"conversations,omitempty"`
}

// Service dispatches only fixed destinations and scoped, typed reads.
type Service struct {
	catalog      commandCatalog
	tools        map[string]tool
	identity     *identity.Service
	applications *applications.Service
}

// New binds the command layer to the application projection's identity owner.
func New(apps *applications.Service) (*Service, error) {
	if apps == nil || apps.Identity == nil {
		return nil, errors.New("PRODUCT_AGENT_IDENTITY_REQUIRED")
	}
	catalog, err := loadCommands(locales)
	if err != nil {
		return nil, err
	}
	tools, err := loadTools()
	if err != nil {
		return nil, err
	}
	return &Service{catalog: catalog, tools: tools, identity: apps.Identity, applications: apps}, nil
}

// Commands lists the exact phrases for the authenticated entrance and the
// requested locale. Run still verifies current workspace and resource access.
func (s *Service) Commands(ctx context.Context, actor identity.Auth, locale string) ([]CommandHelp, error) {
	audience := actor.Me.Audience
	if ctx == nil || audience != "admin" && audience != "portal" {
		return nil, ErrCommandDenied
	}
	phrases, ok := s.catalog.locales[locale]
	if !ok {
		return nil, ErrUnsupportedCommandLocale
	}
	if err := s.withSession(ctx, actor, nil); err != nil {
		return nil, err
	}
	result := make([]CommandHelp, 0, len(phrases))
	for _, id := range slices.Sorted(maps.Keys(phrases)) {
		cmd := commands[id]
		cmd.id = id
		if _, err := s.admit(cmd, actor, ""); err == nil {
			result = append(result, CommandHelp{ID: id, Aliases: slices.Clone(phrases[id])})
		}
	}
	return result, nil
}

// Run executes a command from authenticated server context with a new
// server-generated correlation ID.
func (s *Service) Run(ctx context.Context, actor identity.Auth, input, cursor string) (Result, error) {
	return s.RunForRequest(ctx, actor, input, cursor, uuid.NewString())
}

// RunForRequest uses the correlation ID assigned by the trusted request handler.
// A cursor belongs to a preceding page and is checked by the projection owner.
func (s *Service) RunForRequest(ctx context.Context, actor identity.Auth, input, cursor, requestID string) (Result, error) {
	if ctx == nil || actor.Me.Audience != "admin" && actor.Me.Audience != "portal" {
		return Result{}, ErrCommandDenied
	}
	if !wire.ValidID(requestID) {
		return Result{}, ErrInvalidRequestID
	}
	normalized := normalize(input)
	if normalized == "" {
		return Result{}, ErrInvalidCommand
	}
	cmd, ok := s.catalog.aliases[normalized]
	if !ok {
		return Result{}, ErrUnknownCommand
	}

	path, err := s.admit(cmd, actor, cursor)
	if err != nil {
		return Result{}, err
	}
	switch cmd.risk {
	case riskRead:
		return s.readApplications(ctx, actor, cmd.id, cursor, requestID)
	case riskNavigate:
		if err := s.auditNavigation(ctx, actor, cmd.id, requestID); err != nil {
			return Result{}, err
		}
		return Result{Command: cmd.id, Path: path}, nil
	default:
		return Result{}, ErrCommandDenied
	}
}

func (s *Service) withSession(ctx context.Context, actor identity.Auth, operation func(context.Context, transaction.Executor) error) error {
	store, err := s.identity.Repository()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.identity.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	return store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		if err := s.identity.RecheckOrderedWithin(ctx, tx, actor); err != nil || operation == nil {
			return err
		}
		return operation(ctx, tx)
	})
}

func (s *Service) auditNavigation(ctx context.Context, actor identity.Auth, id, requestID string) error {
	return s.withSession(ctx, actor, func(ctx context.Context, tx transaction.Executor) error {
		w, err := s.applications.Workspaces.ReadScopeWithin(ctx, tx, actor, actor.Me.Workspace.ID)
		if err != nil {
			return err
		}
		return audit.WriteProductAssistantWithin(ctx, tx, audit.ProductAssistantEvent{
			SubjectUserID: actor.Me.User.ID,
			TenantID:      w.TenantID,
			WorkspaceID:   w.ID,
			Audience:      actor.Me.Audience,
			RequestID:     requestID,
			ToolID:        id,
			Outcome:       "success",
		})
	})
}

func (s *Service) readApplications(ctx context.Context, actor identity.Auth, id, cursor, requestID string) (Result, error) {
	query := url.Values{}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	switch id {
	case listApplications:
		page, err := s.applications.ListForAssistant(ctx, actor, query, requestID)
		if err != nil {
			return Result{}, err
		}
		for i := range page.Items {
			page.Items[i].Name = safeDisplayName(page.Items[i].Name)
		}
		return Result{Command: id, Applications: &page}, nil
	case listConversations:
		page, err := s.applications.ConversationsForAssistant(ctx, actor, query, requestID)
		if err != nil {
			return Result{}, err
		}
		for i := range page.Items {
			page.Items[i].WorkflowName = safeDisplayName(page.Items[i].WorkflowName)
		}
		return Result{Command: id, Conversations: &page}, nil
	default:
		return Result{}, ErrCommandDenied
	}
}
