package cli

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"

	"github.com/spf13/cobra"
)

var aliases = resource{key: "aliases", columns: []column{
	{"NAME", "name"}, {"ID", "id"}, {"GATEWAY ID", "gateway_id"}, {"DEPLOYMENT ID", "deployment_id"},
	{"REVISION", "revision"}, {"AVAILABLE", "available"}, {"DISABLED", "disabled_at"},
}}

var gateways = resource{key: "gateways", columns: []column{
	{"NAME", "name"}, {"ID", "id"}, {"NODE ID", "node_id"}, {"ORIGIN", "client_origin"}, {"STATE", "state"},
	{"DELIVERY", "delivery"}, {"DELIVERY STATE", "delivery_state"}, {"DELIVERY ERROR", "delivery_error_code"},
}}

var aliasName = regexp.MustCompile(`^[a-z][a-z0-9.-]{0,63}$`)

func (opts *options) aliasCommand(workspace *string) *cobra.Command {
	group := &cobra.Command{
		Use:     "alias",
		Short:   "Manage publication records for provisioned model gateways.",
		Example: "  ownsolid deployment alias list",
	}
	group.AddCommand(
		opts.distributionListCommand(workspace, aliases, "list", "List aliases in the selected workspace."),
		opts.distributionListCommand(workspace, gateways, "gateways", "List provisioned gateways in the selected workspace."),
		&cobra.Command{
			Use:     "get <alias-id>",
			Short:   "Read a published alias and its revision.",
			Example: "  ownsolid deployment alias get \"$ALIAS_ID\" --output json",
			Args:    cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				if err := requireID(args[0], "alias ID"); err != nil {
					return err
				}
				path, scope, client, err := opts.distributionClient(cmd, *workspace, aliases)
				if err != nil {
					return err
				}
				defer client.http.CloseIdleConnections()
				return opts.renderDetail(cmd.OutOrStdout(), cmd.Context(), client, aliases, path, args[0], scope)
			},
		},
		opts.provisionManagedGatewayCommand(workspace), opts.publishAliasCommand(workspace), opts.disableAliasCommand(workspace),
		opts.aliasCutoverCommand(workspace),
	)
	return group
}

func (opts *options) distributionClient(cmd *cobra.Command, workspace string, resource resource) (string, string, *apiClient, error) {
	cfg, client, err := opts.operator(cmd, workspace)
	if err != nil {
		return "", "", nil, err
	}
	scope, err := client.workspace(cmd.Context(), cfg, workspace)
	if err != nil {
		client.http.CloseIdleConnections()
		return "", "", nil, err
	}
	return cfg.adminPath(resourcePathDistribution) + "/workspaces/" + scope + "/" + resource.key, scope, client, nil
}

var resourcePathDistribution = resource{key: "distribution"}

func (opts *options) distributionListCommand(workspace *string, resource resource, name, description string) *cobra.Command {
	return &cobra.Command{
		Use:     name,
		Short:   description,
		Example: "  ownsolid deployment alias " + name + " --output json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, _, client, err := opts.distributionClient(cmd, *workspace, resource)
			if err != nil {
				return err
			}
			defer client.http.CloseIdleConnections()
			items, err := client.list(cmd.Context(), path, make(url.Values), true)
			if err != nil {
				return err
			}
			return render(cmd.OutOrStdout(), opts.output, resource.columns, items, items)
		},
	}
}

func (opts *options) publishAliasCommand(workspace *string) *cobra.Command {
	var gateway, name, key, reason string
	var revision int64
	cmd := &cobra.Command{
		Use:     "publish <deployment-id>",
		Short:   "Publish a model alias for a provisioned gateway and ready deployment revision.",
		Example: "  ownsolid deployment alias publish \"$DEPLOYMENT_ID\" --gateway \"$GATEWAY_ID\" --name qwen3 --revision \"$REVISION\" --idempotency-key \"$REQUEST_KEY\" --reason 'Publish approved model'",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, field := range []struct{ value, label string }{
				{args[0], "deployment ID"}, {gateway, "--gateway"}, {key, "--idempotency-key"},
			} {
				if err := requireID(field.value, field.label); err != nil {
					return err
				}
			}
			if !aliasName.MatchString(name) {
				return errors.New("--name must start with a lowercase letter and contain at most 64 lowercase letters, digits, dots, or hyphens")
			}
			if err := requireVersion(revision, "--revision"); err != nil {
				return err
			}
			why, err := requiredText(reason, "--reason", 500)
			if err != nil {
				return err
			}
			path, scope, client, err := opts.distributionClient(cmd, *workspace, aliases)
			if err != nil {
				return err
			}
			defer client.http.CloseIdleConnections()
			payload := struct {
				Deployment string `json:"deployment_id"`
				Gateway    string `json:"gateway_id"`
				Name       string `json:"name"`
				Revision   int64  `json:"deployment_revision"`
				Reason     string `json:"reason"`
			}{args[0], gateway, name, revision, why}
			return opts.writeAlias(cmd, client, scope, "", request{method: http.MethodPost, path: path, key: key, status: http.StatusOK}, payload)
		},
	}
	cmd.Flags().StringVar(&gateway, "gateway", "", "Provisioned gateway UUID from deployment alias gateways (required)")
	cmd.Flags().StringVar(&name, "name", "", "Alias name that callers use (required)")
	cmd.Flags().Int64Var(&revision, "revision", 0, "Current deployment revision from deployment get --output json")
	cmd.Flags().StringVar(&key, "idempotency-key", "", "Request UUID (required, reuse the original key on retry)")
	cmd.Flags().StringVar(&reason, "reason", "", "Reason for the audit record (required)")
	return cmd
}

func (opts *options) disableAliasCommand(workspace *string) *cobra.Command {
	var revision int64
	var reason string
	var confirm bool
	cmd := &cobra.Command{
		Use:     "disable <alias-id>",
		Short:   "Disable an alias and prevent new requests through it.",
		Example: "  ownsolid deployment alias disable \"$ALIAS_ID\" --revision \"$ALIAS_REVISION\" --reason 'Withdraw published model' --confirm",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !confirm {
				return errors.New("disabling the alias prevents new requests; pass --confirm to proceed")
			}
			id := args[0]
			if err := requireID(id, "alias ID"); err != nil {
				return err
			}
			if err := requireVersion(revision, "--revision"); err != nil {
				return err
			}
			why, err := requiredText(reason, "--reason", 500)
			if err != nil {
				return err
			}
			path, scope, client, err := opts.distributionClient(cmd, *workspace, aliases)
			if err != nil {
				return err
			}
			defer client.http.CloseIdleConnections()
			payload := struct {
				Revision int64  `json:"expected_revision"`
				Reason   string `json:"reason"`
			}{revision, why}
			return opts.writeAlias(cmd, client, scope, id, request{method: http.MethodPost, path: path + "/" + id + "/disable", status: http.StatusOK}, payload)
		},
	}
	cmd.Flags().Int64Var(&revision, "revision", 0, "Current alias revision from deployment alias get --output json")
	cmd.Flags().StringVar(&reason, "reason", "", "Reason for the audit record (required)")
	cmd.Flags().BoolVar(&confirm, "confirm", false, "Confirm disabling requests through the alias")
	return cmd
}

func (opts *options) writeAlias(cmd *cobra.Command, client *apiClient, scope, id string, req request, input any) error {
	var raw json.RawMessage
	if err := client.mutation(cmd.Context(), req, input, &raw); err != nil {
		return err
	}
	if err := validateScope(raw, id, scope); err != nil {
		return err
	}
	return opts.renderItem(cmd, aliases.columns, raw)
}
