package cli

import "github.com/spf13/cobra"

var gatewayRequests = resource{key: "requests", columns: []column{
	{"ID", "id"}, {"STATE", "state"}, {"ALIAS", "alias_name"}, {"ADMITTED", "admitted_at"},
}}

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
	var keyID string
	var paging pageOptions
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "Read one page of gateway request history.",
		Example: "  ownsolid gateway request list --limit 20 --api-key-id \"$KEY_ID\" --output json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			query, err := paging.query(cmd)
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("api-key-id") {
				if err := requireID(keyID, "--api-key-id"); err != nil {
					return err
				}
				query.Set("api_key_id", keyID)
			}

			path, scope, client, err := opts.distributionClient(cmd, *workspace, gatewayRequests)
			if err != nil {
				return err
			}
			defer client.http.CloseIdleConnections()
			var page boundedPage
			if err := client.get(cmd.Context(), path, query, &page); err != nil {
				return err
			}
			if err := paging.validate(page, "gateway request"); err != nil {
				return err
			}
			for _, item := range page.Items {
				if err := validateScope(item, "", scope); err != nil {
					return err
				}
			}
			return opts.renderPage(cmd, gatewayRequests.columns, page)
		},
	}
	cmd.Flags().StringVar(&keyID, "api-key-id", "", "Return requests made with this API key UUID")
	paging.bind(cmd, "Next cursor from the same workspace and API key filter")
	return cmd
}
