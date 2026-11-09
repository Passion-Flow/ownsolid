package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/spf13/cobra"
)

const (
	gatewayRequestMaxPage   = 100
	gatewayRequestMaxCursor = 512
)

var gatewayRequests = resource{key: "requests", columns: []column{
	{"ID", "id"}, {"STATE", "state"}, {"ALIAS", "alias_name"}, {"ADMITTED", "admitted_at"},
}}

type gatewayRequestPage struct {
	Items      []json.RawMessage `json:"items"`
	NextCursor *string           `json:"next_cursor"`
}

func (opts *options) gatewayCommand() *cobra.Command {
	group := &cobra.Command{
		Use:     "gateway",
		Short:   "Inspect model gateway request history.",
		Example: "  ownsolid gateway request list",
	}
	requests := &cobra.Command{
		Use:     "request",
		Short:   "Read authorized requests in the selected workspace.",
		Example: "  ownsolid gateway request get \"$REQUEST_ID\" --output json",
	}
	var workspace string
	requests.PersistentFlags().StringVar(&workspace, "workspace", "", "Workspace UUID (defaults to the configured or current workspace)")
	requests.AddCommand(opts.listGatewayRequests(&workspace), &cobra.Command{
		Use:     "get <request-id>",
		Short:   "Read the gateway request and its recorded outcome.",
		Example: "  ownsolid gateway request get \"$REQUEST_ID\" --output json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if err := requireID(id, "request ID"); err != nil {
				return err
			}
			path, scope, client, err := opts.distributionClient(cmd, workspace, gatewayRequests)
			if err != nil {
				return err
			}
			defer client.http.CloseIdleConnections()
			return opts.renderDetail(cmd.OutOrStdout(), cmd.Context(), client, gatewayRequests, path, id, scope)
		},
	})
	group.AddCommand(requests)
	return group
}

func (opts *options) listGatewayRequests(workspace *string) *cobra.Command {
	var keyID, cursor string
	var limit int
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "Read one page of gateway request history.",
		Example: "  ownsolid gateway request list --limit 20 --api-key-id \"$KEY_ID\" --output json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			query := make(url.Values)
			if cmd.Flags().Changed("limit") {
				if limit < 1 || limit > gatewayRequestMaxPage {
					return fmt.Errorf("--limit must be between 1 and %d", gatewayRequestMaxPage)
				}
				query.Set("limit", strconv.Itoa(limit))
			}
			if cmd.Flags().Changed("api-key-id") {
				if err := requireID(keyID, "--api-key-id"); err != nil {
					return err
				}
				query.Set("api_key_id", keyID)
			}
			if cmd.Flags().Changed("cursor") {
				if !validGatewayRequestCursor(cursor) {
					return fmt.Errorf("--cursor must be printable and between 1 and %d bytes; use the previous page's next cursor", gatewayRequestMaxCursor)
				}
				query.Set("cursor", cursor)
			}

			path, scope, client, err := opts.distributionClient(cmd, *workspace, gatewayRequests)
			if err != nil {
				return err
			}
			defer client.http.CloseIdleConnections()
			var page gatewayRequestPage
			if err := client.get(cmd.Context(), path, query, &page); err != nil {
				return err
			}
			if page.Items == nil || len(page.Items) > limit {
				return errors.New("the server returned an invalid gateway request page")
			}
			if next := page.NextCursor; next != nil &&
				(len(page.Items) == 0 || *next == cursor || !validGatewayRequestCursor(*next)) {
				return errors.New("the server returned an invalid gateway request cursor")
			}
			for _, item := range page.Items {
				if err := validateScope(item, "", scope); err != nil {
					return err
				}
			}
			if err := render(cmd.OutOrStdout(), opts.output, gatewayRequests.columns, page, page.Items); err != nil {
				return err
			}
			if opts.output == "table" && page.NextCursor != nil {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "\nNext cursor: %q\n", *page.NextCursor)
			}
			return err
		},
	}
	cmd.Flags().StringVar(&keyID, "api-key-id", "", "Return requests made with this API key UUID")
	cmd.Flags().StringVar(&cursor, "cursor", "", "Next cursor from the same workspace and API key filter")
	cmd.Flags().IntVar(&limit, "limit", gatewayRequestMaxPage, "Maximum rows to read (the server selects the page size when omitted)")
	return cmd
}

func validGatewayRequestCursor(value string) bool {
	return value != "" && len(value) <= gatewayRequestMaxCursor &&
		!strings.ContainsFunc(value, func(r rune) bool { return !unicode.IsPrint(r) })
}
