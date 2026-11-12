package cli

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

const userReadPath = "/api/v1/admin/users"

var userColumns = []column{
	{"ID", "id"}, {"EMAIL", "email"}, {"NAME", "display_name"},
	{"STATUS", "status"}, {"ADMIN", "is_platform_admin"},
}

func (opts *options) userCommand() *cobra.Command {
	group := &cobra.Command{
		Use: "user", Short: "Inspect the installation account directory.",
		Example: "  ownsolid user list --status disabled --output json",
	}
	group.AddCommand(opts.listUsers(), &cobra.Command{
		Use: "get <user-id>", Short: "Read an account by its exact UUID.",
		Example: "  ownsolid user get \"$USER_ID\" --output yaml",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if !resourceID.MatchString(id) || id == "00000000-0000-0000-0000-000000000000" {
				return errors.New("user ID must be a canonical lowercase nonzero UUID")
			}
			_, client, err := opts.operator(cmd, "")
			if err != nil {
				return err
			}
			defer client.http.CloseIdleConnections()
			var result json.RawMessage
			if err := client.get(cmd.Context(), userReadPath+"/"+id, nil, &result); err != nil {
				return err
			}
			if err := validateUserRead(result, id); err != nil {
				return err
			}
			return opts.renderItem(cmd, userColumns, result)
		},
	})
	return group
}

func (opts *options) listUsers() *cobra.Command {
	var paging pageOptions
	var prefix, search, status string
	cmd := &cobra.Command{
		Use: "list", Short: "Read one page of accounts through the Admin entrance.",
		Example: "  ownsolid user list --email-prefix research --status active --limit 20 --output json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			query, err := paging.query(cmd)
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("email-prefix") {
				if len(prefix) < 1 || len(prefix) > 254 ||
					strings.ContainsFunc(prefix, func(r rune) bool { return r < 32 || r >= 127 }) {
					return errors.New("--email-prefix must contain 1 to 254 printable ASCII characters")
				}
				query.Set("email_prefix", prefix)
			}
			if cmd.Flags().Changed("query") {
				if search == "" || !utf8.ValidString(search) || utf8.RuneCountInString(search) > 254 ||
					strings.ContainsFunc(search, unicode.IsControl) {
					return errors.New("--query must contain 1 to 254 UTF-8 characters without control characters")
				}
				query.Set("q", search)
			}
			if cmd.Flags().Changed("status") {
				if status != "active" && status != "disabled" {
					return errors.New("--status must be active or disabled")
				}
				query.Set("status", status)
			}
			_, client, err := opts.operator(cmd, "")
			if err != nil {
				return err
			}
			defer client.http.CloseIdleConnections()
			var page boundedPage
			if err := client.get(cmd.Context(), userReadPath, query, &page); err != nil {
				return err
			}
			if err := paging.validate(page, "user"); err != nil {
				return err
			}
			for _, item := range page.Items {
				if err := validateUserRead(item, ""); err != nil {
					return err
				}
			}
			return opts.renderPage(cmd, userColumns, page)
		},
	}
	cmd.Flags().StringVar(&prefix, "email-prefix", "", "Case-insensitive literal prefix of the account email")
	cmd.Flags().StringVar(&search, "query", "", "Case-insensitive literal text anywhere in email or display name")
	cmd.Flags().StringVar(&status, "status", "", "Account status: active or disabled")
	paging.bind(cmd, "Next cursor from the same account directory filters and Admin origin")
	return cmd
}

func validateUserRead(item json.RawMessage, expected string) error {
	id, err := readResourceID(item)
	if err != nil || !resourceID.MatchString(id) || id == "00000000-0000-0000-0000-000000000000" ||
		expected != "" && id != expected {
		return errors.New("the server returned a different or invalid user ID; check CLI/server compatibility and retry")
	}
	return nil
}
