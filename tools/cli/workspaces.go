package cli

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

const workspaceReadPath = "/api/v1/admin/workspaces"

var workspaceColumns = []column{
	{"NAME", "name"}, {"ID", "id"}, {"STATUS", "status"}, {"DEFAULT", "is_default"}, {"ROLE", "role"},
}

func (opts *options) workspaceCommand() *cobra.Command {
	group := &cobra.Command{
		Use: "workspace", Short: "Inspect workspaces available to the platform administrator.",
		Example: "  ownsolid workspace list --output json",
	}
	group.AddCommand(opts.listWorkspaces(), &cobra.Command{
		Use: "get <workspace-id>", Short: "Read a workspace by its exact UUID.",
		Example: "  ownsolid workspace get \"$WORKSPACE_ID\" --output yaml",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if !validWorkspaceReadID(id) {
				return errors.New("workspace ID must be a canonical lowercase nonzero UUID")
			}
			_, client, err := opts.operator(cmd, "")
			if err != nil {
				return err
			}
			defer client.http.CloseIdleConnections()
			var result json.RawMessage
			if err := client.get(cmd.Context(), workspaceReadPath+"/"+id, nil, &result); err != nil {
				return err
			}
			if err := validateWorkspaceRead(result, id); err != nil {
				return err
			}
			return opts.renderItem(cmd, workspaceColumns, result)
		},
	})
	return group
}

func (opts *options) listWorkspaces() *cobra.Command {
	var search string
	var paging pageOptions
	cmd := &cobra.Command{
		Use: "list", Short: "Read one page of workspaces in the installed tenant.",
		Example: "  ownsolid workspace list --query research --limit 20 --output json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			query, err := paging.query(cmd)
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("query") {
				if search == "" || !utf8.ValidString(search) || utf8.RuneCountInString(search) > 100 ||
					strings.ContainsFunc(search, unicode.IsControl) {
					return errors.New("--query must contain 1 to 100 UTF-8 characters without control characters")
				}
				query.Set("q", search)
			}
			_, client, err := opts.operator(cmd, "")
			if err != nil {
				return err
			}
			defer client.http.CloseIdleConnections()
			var page boundedPage
			if err := client.get(cmd.Context(), workspaceReadPath, query, &page); err != nil {
				return err
			}
			if err := paging.validate(page, "workspace"); err != nil {
				return err
			}
			for _, item := range page.Items {
				if err := validateWorkspaceRead(item, ""); err != nil {
					return err
				}
			}
			return opts.renderPage(cmd, workspaceColumns, page)
		},
	}
	cmd.Flags().StringVar(&search, "query", "", "Case-insensitive literal text anywhere in the workspace name")
	paging.bind(cmd, "Next cursor from the same administrator and name query")
	return cmd
}

func validWorkspaceReadID(id string) bool {
	return resourceID.MatchString(id) && id != "00000000-0000-0000-0000-000000000000"
}

func validateWorkspaceRead(item json.RawMessage, expected string) error {
	id, err := readResourceID(item)
	if err != nil || !validWorkspaceReadID(id) || expected != "" && id != expected {
		return errors.New("the server returned a different or invalid workspace ID; check CLI/server compatibility and retry")
	}
	return nil
}
