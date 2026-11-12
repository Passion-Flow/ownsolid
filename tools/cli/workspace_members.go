package cli

import (
	"encoding/json"
	"errors"

	"github.com/spf13/cobra"
)

var workspaceMemberColumns = []column{
	{"NAME", "display_name"}, {"EMAIL", "email"}, {"USER ID", "user_id"},
	{"ROLE", "role"}, {"STATUS", "user_status"},
}

func (opts *options) workspaceMemberCommand() *cobra.Command {
	group := &cobra.Command{
		Use: "member", Short: "Inspect the members of an exact workspace.",
		Example: "  ownsolid workspace member list \"$WORKSPACE_ID\" --output json",
	}
	var paging pageOptions
	list := &cobra.Command{
		Use: "list <workspace-id>", Short: "Read one page of workspace members.",
		Example: "  ownsolid workspace member list \"$WORKSPACE_ID\" --limit 20 --output json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if !validWorkspaceReadID(id) {
				return errors.New("workspace ID must be a canonical lowercase nonzero UUID")
			}
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
			if err := client.get(cmd.Context(), workspaceReadPath+"/"+id+"/members", query, &page); err != nil {
				return err
			}
			if err := paging.validate(page, "workspace member"); err != nil {
				return err
			}
			for _, row := range page.Items {
				var member struct {
					UserID string `json:"user_id"`
				}
				if err := json.Unmarshal(row, &member); err != nil || !resourceID.MatchString(member.UserID) ||
					member.UserID == "00000000-0000-0000-0000-000000000000" {
					return errors.New("the server returned an invalid member user ID; check CLI/server compatibility and retry")
				}
			}
			return opts.renderPage(cmd, workspaceMemberColumns, page)
		},
	}
	paging.bind(list, "Next cursor from the same workspace, administrator and entrance")
	group.AddCommand(list)
	return group
}
