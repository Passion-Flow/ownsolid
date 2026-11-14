package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/spf13/cobra"
)

type aliasCutoverInput struct {
	Action             string  `json:"action"`
	ExpectedRevision   int64   `json:"expected_revision"`
	DeploymentID       *string `json:"deployment_id"`
	DeploymentRevision *int64  `json:"deployment_revision"`
	SourceRevision     *int64  `json:"source_revision"`
	Reason             string  `json:"reason"`
}

func (opts *options) aliasCutoverCommand(workspace *string) *cobra.Command {
	group := &cobra.Command{
		Use:     "cutover",
		Short:   "Inspect and change a model alias target.",
		Example: "  ownsolid deployment alias cutover history \"$ALIAS_ID\"",
	}
	group.AddCommand(opts.aliasTargetHistoryCommand(workspace), opts.aliasCutoverAction(workspace, false), opts.aliasCutoverAction(workspace, true))
	return group
}

func (opts *options) aliasCutoverAction(workspace *string, apply bool) *cobra.Command {
	var revision, deploymentRevision, sourceRevision int64
	var deploymentID, reason, key string
	var confirm bool
	cmd := &cobra.Command{
		Use:     "preview <alias-id> <pause|resume|retarget|rollback>",
		Short:   "Show the alias state before and after a proposed cutover.",
		Example: "  ownsolid deployment alias cutover preview \"$ALIAS_ID\" pause --revision \"$REVISION\" --reason 'Maintenance'",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireID(args[0], "alias ID"); err != nil {
				return err
			}
			if err := requireVersion(revision, "--revision"); err != nil {
				return err
			}
			why, err := requiredText(reason, "--reason", 500)
			if err != nil {
				return err
			}
			input := aliasCutoverInput{Action: args[1], ExpectedRevision: revision, Reason: why}
			switch input.Action {
			case "pause", "resume":
				if cmd.Flags().Changed("deployment") || cmd.Flags().Changed("deployment-revision") {
					return errors.New("pause and resume use the current target; omit --deployment and --deployment-revision")
				}
			case "retarget", "rollback":
				if err := requireID(deploymentID, "--deployment"); err != nil {
					return err
				}
				if err := requireVersion(deploymentRevision, "--deployment-revision"); err != nil {
					return err
				}
				input.DeploymentID, input.DeploymentRevision = &deploymentID, &deploymentRevision
			default:
				return errors.New("choose pause, resume, retarget, or rollback")
			}
			if input.Action == "rollback" {
				if sourceRevision < 1 || sourceRevision >= revision {
					return errors.New("--source-revision must be a positive alias history revision older than --revision")
				}
				input.SourceRevision = &sourceRevision
			} else if cmd.Flags().Changed("source-revision") {
				return errors.New("--source-revision is only valid for rollback")
			}
			if apply {
				if !confirm {
					return errors.New("cutover affects callers of the alias; review the preview and pass --confirm")
				}
				if err := requireID(key, "--idempotency-key"); err != nil {
					return err
				}
			}
			return opts.runAliasCutover(cmd, *workspace, args[0], key, input)
		},
	}
	cmd.Flags().Int64Var(&revision, "revision", 0, "Current alias revision (required)")
	cmd.Flags().StringVar(&reason, "reason", "", "Reason for the cutover (required)")
	cmd.Flags().StringVar(&deploymentID, "deployment", "", "Ready target deployment UUID for retarget or rollback")
	cmd.Flags().Int64Var(&deploymentRevision, "deployment-revision", 0, "Current target deployment revision for retarget or rollback")
	cmd.Flags().Int64Var(&sourceRevision, "source-revision", 0, "Recorded alias revision to restore on rollback")
	if apply {
		cmd.Use = "apply <alias-id> <pause|resume|retarget|rollback>"
		cmd.Short = "Apply a cutover after reviewing its preview."
		cmd.Example = "  ownsolid deployment alias cutover apply \"$ALIAS_ID\" pause --revision \"$REVISION\" --reason 'Maintenance' --idempotency-key \"$REQUEST_KEY\" --confirm"
		cmd.Flags().StringVar(&key, "idempotency-key", "", "Request UUID (required, reuse the original key on retry)")
		cmd.Flags().BoolVar(&confirm, "confirm", false, "Confirm the change can affect model callers")
	}
	return cmd
}

var aliasCutoverResultColumns = []column{{"APPLIED REVISION", "applied_revision"}, {"REPLAYED", "replayed"}}

func (opts *options) runAliasCutover(cmd *cobra.Command, workspace, id, key string, input aliasCutoverInput) error {
	path, scope, client, err := opts.distributionClient(cmd, workspace, aliases)
	if err != nil {
		return err
	}
	defer client.http.CloseIdleConnections()
	req := request{method: http.MethodPost, path: path + "/" + id + "/cutovers", key: key, status: http.StatusOK}
	if key == "" {
		req.path += "/preview"
		return opts.previewAliasCutover(cmd, client, req, input)
	}
	var raw json.RawMessage
	if err := client.mutation(cmd.Context(), req, input, &raw); err != nil {
		return err
	}
	var result struct {
		Alias           json.RawMessage `json:"alias"`
		AppliedRevision int64           `json:"applied_revision"`
		Replayed        *bool           `json:"replayed"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || result.AppliedRevision != input.ExpectedRevision+1 || result.Replayed == nil {
		return errors.New("the server returned an invalid cutover result; inspect the alias before applying another change")
	}
	if err := validateScope(result.Alias, id, scope); err != nil {
		return err
	}
	return opts.renderItem(cmd, aliasCutoverResultColumns, raw)
}

type aliasCutoverState struct {
	Revision           int64  `json:"revision"`
	DeploymentID       string `json:"deployment_id"`
	DeploymentRevision int64  `json:"deployment_revision"`
	Paused             *bool  `json:"paused"`
}

var aliasCutoverStateColumns = []column{
	{"STATE", "state"}, {"REVISION", "revision"}, {"DEPLOYMENT ID", "deployment_id"},
	{"DEPLOYMENT REVISION", "deployment_revision"}, {"PAUSED", "paused"},
}

func (opts *options) previewAliasCutover(cmd *cobra.Command, client *apiClient, req request, input aliasCutoverInput) error {
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	req.body = body
	if err := client.prepareWrite(cmd.Context(), &req); err != nil {
		return err
	}
	var result struct {
		Before aliasCutoverState `json:"before"`
		After  aliasCutoverState `json:"after"`
	}
	if err := client.request(cmd.Context(), req, &result); err != nil {
		return err
	}
	if result.Before.Revision != input.ExpectedRevision || result.After.Revision != input.ExpectedRevision+1 {
		return errors.New("the server returned unexpected preview revisions")
	}
	rows := make([]json.RawMessage, 0, 2)
	for _, state := range []struct {
		Name string `json:"state"`
		aliasCutoverState
	}{{"before", result.Before}, {"after", result.After}} {
		if !resourceID.MatchString(state.DeploymentID) || state.DeploymentRevision < 1 ||
			state.DeploymentRevision > maxResourceVersion || state.Paused == nil {
			return errors.New("the server returned an invalid preview state")
		}
		row, err := json.Marshal(state)
		if err != nil {
			return err
		}
		rows = append(rows, row)
	}
	return render(cmd.OutOrStdout(), opts.output, aliasCutoverStateColumns, result, rows)
}

var aliasTargetColumns = []column{
	{"REVISION", "revision"}, {"ACTION", "action"}, {"DEPLOYMENT ID", "deployment_id"},
	{"DEPLOYMENT REVISION", "deployment_revision"}, {"SOURCE REVISION", "source_revision"},
}

func (opts *options) aliasTargetHistoryCommand(workspace *string) *cobra.Command {
	var paging pageOptions
	cmd := &cobra.Command{
		Use:     "history <alias-id>",
		Short:   "Read one page of immutable alias target revisions.",
		Example: "  ownsolid deployment alias cutover history \"$ALIAS_ID\" --limit 10",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if err := requireID(id, "alias ID"); err != nil {
				return err
			}
			query, err := paging.query(cmd)
			if err != nil {
				return err
			}
			path, _, client, err := opts.distributionClient(cmd, *workspace, aliases)
			if err != nil {
				return err
			}
			defer client.http.CloseIdleConnections()
			var page boundedPage
			if err := client.get(cmd.Context(), path+"/"+id+"/targets", query, &page); err != nil {
				return err
			}
			if err := paging.validate(page, "alias target history"); err != nil {
				return err
			}
			for _, item := range page.Items {
				var target struct {
					AliasID string `json:"alias_id"`
				}
				if err := json.Unmarshal(item, &target); err != nil || target.AliasID != id {
					return fmt.Errorf("the server returned a target outside alias %s", id)
				}
			}
			return opts.renderPage(cmd, aliasTargetColumns, page)
		},
	}
	paging.bind(cmd, "Next cursor from the same workspace and alias")
	return cmd
}
