package productagent

import (
	"context"
	"errors"
	"maps"
	"net/url"
	"slices"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/applications"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

var (
	// ErrInvalidCommand means the text or cursor does not meet the command contract.
	ErrInvalidCommand = errors.New("PRODUCT_AGENT_COMMAND_INVALID")
	// ErrUnknownCommand means the text did not match a registered alias.
	ErrUnknownCommand = errors.New("PRODUCT_AGENT_COMMAND_UNKNOWN")
	// ErrCommandDenied means the caller's entrance does not admit the command.
	ErrCommandDenied = errors.New("ACCESS_DENIED")
	// ErrWorkspaceRequired means the authenticated portal has no active workspace.
	ErrWorkspaceRequired = errors.New("PRODUCT_AGENT_WORKSPACE_REQUIRED")
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
	if err := s.recheck(ctx, actor); err != nil {
		return nil, err
	}
	result := make([]CommandHelp, 0, len(phrases))
	for _, id := range slices.Sorted(maps.Keys(phrases)) {
		canNavigate := commands[id].destination(audience) != ""
		canRead := actor.Me.Workspace != nil && s.tools[id].allowsRead(audience)
		if canNavigate || canRead {
			result = append(result, CommandHelp{ID: id, Aliases: slices.Clone(phrases[id])})
		}
	}
	return result, nil
}

// Run accepts the authenticated server context. A cursor belongs to a preceding
// page of the same read command and is checked by the owner of that projection.
func (s *Service) Run(ctx context.Context, actor identity.Auth, input, cursor string) (Result, error) {
	if ctx == nil || actor.Me.Audience != "admin" && actor.Me.Audience != "portal" {
		return Result{}, ErrCommandDenied
	}
	normalized := normalize(input)
	if normalized == "" {
		return Result{}, ErrInvalidCommand
	}
	cmd, ok := s.catalog.aliases[normalized]
	if !ok {
		return Result{}, ErrUnknownCommand
	}

	switch cmd.id {
	case listApplications, listConversations:
		return s.readApplications(ctx, actor, cmd.id, cursor)
	default:
		path := cmd.destination(actor.Me.Audience)
		if path == "" {
			return Result{}, ErrCommandDenied
		}
		if cursor != "" {
			return Result{}, ErrInvalidCommand
		}
		if err := s.recheck(ctx, actor); err != nil {
			return Result{}, err
		}
		return Result{Command: cmd.id, Path: path}, nil
	}
}

func (s *Service) recheck(ctx context.Context, actor identity.Auth) error {
	store, err := s.identity.Repository()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.identity.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	return store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		return s.identity.RecheckOrderedWithin(ctx, tx, actor)
	})
}

func (s *Service) readApplications(ctx context.Context, actor identity.Auth, id, cursor string) (Result, error) {
	if !s.tools[id].allowsRead(actor.Me.Audience) {
		return Result{}, ErrCommandDenied
	}
	if actor.Me.Workspace == nil {
		return Result{}, ErrWorkspaceRequired
	}
	query := url.Values{}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	workspace := actor.Me.Workspace.ID
	if id == listApplications {
		page, err := s.applications.List(ctx, actor, workspace, query)
		if err != nil {
			return Result{}, err
		}
		return Result{Command: id, Applications: &page}, nil
	}
	page, err := s.applications.Conversations(ctx, actor, workspace, query)
	if err != nil {
		return Result{}, err
	}
	return Result{Command: id, Conversations: &page}, nil
}
