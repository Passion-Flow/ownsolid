package applications

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"golang.org/x/text/unicode/norm"
)

// MaximumApplicationNameCharacters is the application contract's display-name bound.
const MaximumApplicationNameCharacters = 120

// ApplicationResolution contains current authorized candidates, never an action.
// ApplicationID is populated only when the visible name resolves uniquely.
type ApplicationResolution struct {
	State         string        `json:"state"`
	ApplicationID string        `json:"application_id,omitempty"`
	Candidates    []Application `json:"candidates"`
}

// NormalizeApplicationName follows the writer's NFC display-name normalization.
// Matching remains case-sensitive; the broader workflow name key is not reused.
func NormalizeApplicationName(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", failure("VALIDATION_FAILED")
	}
	value = strings.TrimSpace(norm.NFC.String(value))
	if value == "" || utf8.RuneCountInString(value) > MaximumApplicationNameCharacters || strings.ContainsFunc(value, unicode.IsControl) {
		return "", failure("VALIDATION_FAILED")
	}
	return value, nil
}

// ResolveNameForAssistant resolves a visible name within current server scope.
// It shares publication, membership and metadata-only auditing with ListForAssistant.
// A name, including command-like text, is data and grants no execution authority.
func (s *Service) ResolveNameForAssistant(ctx context.Context, actor identity.Auth, name, requestID string) (ApplicationResolution, error) {
	name, err := NormalizeApplicationName(name)
	if err != nil {
		return ApplicationResolution{}, err
	}
	out := ApplicationResolution{State: "not_found", Candidates: []Application{}}
	err = s.transaction(ctx, actor, assistantWorkspace(actor), &assistantRead{requestID: requestID, toolID: "find_application"},
		func(ctx context.Context, tx transaction.Executor, workspace workspaces.Workspace) error {
			if tx.Dialect() != transaction.PostgreSQL {
				return failure("STORAGE_UNAVAILABLE")
			}
			configuration, err := s.Workflows.ConfigurationWithin(ctx, tx, actor, workspace.ID)
			if err != nil {
				return err
			}
			// Two matches suffice to refuse an implicit selection. C equality
			// avoids locale-dependent case or accent equivalence.
			rows, err := tx.Query(ctx, transaction.SQL(transaction.PostgreSQL, publishedApplicationsPostgreSQL+
				` AND f.name COLLATE "C"=$3::text ORDER BY f.id LIMIT 2`, workspace.TenantID, workspace.ID, name))
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				app, err := scanApplication(rows)
				if err != nil {
					return err
				}
				app.CanExecute = configuration.Enabled && configuration.CanExecute
				app.QuestionBytes = configuration.Limits.QuestionBytes
				out.Candidates = append(out.Candidates, app)
			}
			if err := rows.Err(); err != nil {
				return err
			}
			switch len(out.Candidates) {
			case 1:
				out.State, out.ApplicationID = "resolved", out.Candidates[0].WorkflowID
			case 2:
				out.State = "ambiguous"
			}
			return nil
		})
	if err != nil {
		return ApplicationResolution{}, err
	}
	return out, nil
}

func scanApplication(row interface{ Scan(...any) error }) (Application, error) {
	var app Application
	err := row.Scan(&app.WorkflowID, &app.WorkspaceID, &app.Name, &app.VersionID, &app.VersionNumber, &app.GraphSHA256, &app.PublishedAt)
	app.PublishedAt = app.PublishedAt.UTC()
	return app, err
}
