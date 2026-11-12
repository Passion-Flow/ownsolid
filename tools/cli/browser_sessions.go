package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/spf13/cobra"
)

const browserSessionsPath = "/api/v1/me/sessions"

var browserSessionColumns = []column{
	{"ID", "id"}, {"CURRENT", "current"}, {"METHOD", "authentication_method"},
	{"LAST SEEN", "last_seen_at"}, {"EXPIRES", "absolute_expires_at"},
}

func (opts *options) browserSessionsCommand() *cobra.Command {
	group := &cobra.Command{
		Use: "session", Short: "Inspect and end this account's Admin sessions.",
		Example: "  ownsolid session list --output json",
	}
	var paging pageOptions
	list := &cobra.Command{
		Use: "list", Short: "Read one page of this account's active Admin sessions.",
		Example: "  ownsolid session list --limit 20 --output json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			query, err := paging.query(cmd)
			if err != nil {
				return err
			}
			_, client, err := opts.operator(cmd, "")
			if err != nil {
				return err
			}
			defer client.http.CloseIdleConnections()
			var page boundedPage
			if err := client.get(cmd.Context(), browserSessionsPath, query, &page); err != nil {
				return err
			}
			if err := paging.validate(page, "session"); err != nil {
				return err
			}
			for _, item := range page.Items {
				id, err := readResourceID(item)
				if err != nil || !validBrowserSessionID(id) {
					return errors.New("the server returned an invalid session ID; check CLI/server compatibility and retry")
				}
			}
			return opts.renderPage(cmd, browserSessionColumns, page)
		},
	}
	paging.bind(list, "Next cursor from this account and Admin entrance")
	var confirm bool
	revoke := &cobra.Command{
		Use: "revoke <session-id>", Short: "End an exact session owned by this account.",
		Example: "  ownsolid session revoke \"$SESSION_ID\" --confirm",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !validBrowserSessionID(args[0]) {
				return errors.New("session ID must be a canonical lowercase nonzero UUID from ownsolid session list")
			}
			if !confirm {
				return errors.New("ending a session requires --confirm; inspect ownsolid session list before retrying")
			}
			cfg, client, err := opts.operator(cmd, "")
			if err != nil {
				return err
			}
			defer client.http.CloseIdleConnections()
			return opts.revokeBrowserSession(cmd, cfg, client, args[0])
		},
	}
	revoke.Flags().BoolVar(&confirm, "confirm", false, "Confirm ending the selected session")
	group.AddCommand(list, revoke)
	return group
}

func validBrowserSessionID(id string) bool {
	return resourceID.MatchString(id) && id != "00000000-0000-0000-0000-000000000000"
}

func (opts *options) revokeBrowserSession(cmd *cobra.Command, cfg settings, client *apiClient, id string) error {
	store, err := openSessionStore(cfg, false)
	if err != nil {
		return err
	}
	defer store.root.Close()
	lock, err := store.lock()
	if err != nil {
		return err
	}
	defer lock.Close()
	current, err := store.read()
	if err != nil {
		return err
	}
	if current == "" || current != client.cookie("session") {
		return errors.New("the saved Admin session changed; retry with the current login before ending a session")
	}
	input := request{method: http.MethodDelete, path: browserSessionsPath + "/" + id, status: http.StatusNoContent}
	if err := client.prepareWrite(cmd.Context(), &input); err != nil {
		return err
	}
	if err := client.request(cmd.Context(), input, nil); err != nil {
		var problem *apiError
		if errors.As(err, &problem) && problem.status < 500 && problem.code != "STORAGE_COMMIT_OUTCOME_UNKNOWN" {
			return err
		}
		return fmt.Errorf("session revocation result uncertain; inspect ownsolid session list and retry the same session ID %s while this login remains active: %w", id, err)
	}
	ended := client.cookie("session") == ""
	if ended {
		if err := store.remove(); err != nil {
			return fmt.Errorf("the server ended this session; fix local credential permissions and run ownsolid login: %w", err)
		}
	}
	result, err := json.Marshal(map[string]any{"id": id, "revoked": true, "current_session_ended": ended})
	if err != nil {
		return err
	}
	return opts.renderItem(cmd, []column{{"ID", "id"}, {"REVOKED", "revoked"}, {"CURRENT SESSION ENDED", "current_session_ended"}}, result)
}
