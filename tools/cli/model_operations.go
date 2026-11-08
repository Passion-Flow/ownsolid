package cli

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/spf13/cobra"
)

func (opts *options) modelGetCommand(workspace *string) *cobra.Command {
	return &cobra.Command{
		Use:     "get <model-id>",
		Short:   "Show a model version and its manifest by UUID.",
		Example: "  ownsolid model get \"$MODEL_ID\" --output yaml",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireID(args[0], "model ID"); err != nil {
				return err
			}
			cfg, client, err := opts.operator(cmd, *workspace)
			if err != nil {
				return err
			}
			defer client.http.CloseIdleConnections()
			scope := *workspace
			if scope == "" {
				scope = cfg.WorkspaceID
			}
			return opts.renderDetail(cmd.OutOrStdout(), cmd.Context(), client, models, cfg.adminPath(models), args[0], scope)
		},
	}
}

func (opts *options) retireModelCommand(workspace *string) *cobra.Command {
	var reason string
	var confirm bool
	cmd := &cobra.Command{
		Use:     "retire <model-id>",
		Short:   "Retire a model version from use in new workloads.",
		Example: "  ownsolid model retire \"$MODEL_ID\" --reason 'Superseded by an approved version' --confirm",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !confirm {
				return errors.New("retirement prevents new workloads from using the model; pass --confirm to proceed")
			}
			if err := requireID(args[0], "model ID"); err != nil {
				return err
			}
			why, err := requiredText(reason, "--reason", 500)
			if err != nil {
				return err
			}
			cfg, client, err := opts.operator(cmd, *workspace)
			if err != nil {
				return err
			}
			defer client.http.CloseIdleConnections()
			ctx, id := cmd.Context(), args[0]
			scope, err := client.workspace(ctx, cfg, *workspace)
			if err != nil {
				return err
			}
			path := cfg.adminPath(models)
			if _, err := client.detail(ctx, path, id, scope); err != nil {
				return err
			}
			var result json.RawMessage
			req := request{method: http.MethodPost, path: path + "/" + id + "/retire", status: http.StatusOK}
			if err := client.mutation(ctx, req, map[string]string{"reason": why}, &result); err != nil {
				return err
			}
			if err := validateScope(result, id, scope); err != nil {
				return err
			}
			return opts.renderItem(cmd, []column{{"ID", "id"}, {"STATE", "state"}, {"ARTIFACT RETENTION", "artifact_retention"}}, result)
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "Reason for the audit record (required)")
	cmd.Flags().BoolVar(&confirm, "confirm", false, "Confirm that the model will be retired")
	return cmd
}
