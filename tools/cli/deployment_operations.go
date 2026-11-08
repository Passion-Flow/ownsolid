package cli

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/spf13/cobra"
)

type deploymentCreate struct {
	ModelID string `json:"model_id"`
	NodeID  string `json:"node_id"`
	GPUUUID string `json:"gpu_uuid"`
	Reason  string `json:"reason"`
}

type deploymentAction struct {
	Action   string `json:"action"`
	Revision int64  `json:"revision"`
	Reason   string `json:"reason"`
}

func (opts *options) addDeploymentOperations(group *cobra.Command, workspace *string) {
	group.AddCommand(opts.placementCommand(workspace), opts.createDeploymentCommand(workspace))
	for _, action := range []struct {
		command, action, description string
		confirm                      bool
	}{
		{"start", "deploy", "Request that an existing deployment start.", false},
		{"stop", "stop", "Request that an existing deployment stop serving.", true},
		{"observe", "observe", "Ask the node to report the deployment's state.", false},
		{"verify", "verify", "Ask the node to verify a running deployment.", false},
		{"remove", "remove", "Request removal of a deployment and its runtime.", true},
	} {
		var key, reason string
		var revision int64
		var confirm bool
		cmd := &cobra.Command{
			Use:     action.command + " <deployment-id>",
			Short:   action.description,
			Example: "  ownsolid deployment " + action.command + " \"$DEPLOYMENT_ID\" --revision \"$REVISION\" --idempotency-key \"$REQUEST_KEY\" --reason 'Planned service change'",
			Args:    cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				if action.confirm && !confirm {
					return errors.New("this action can interrupt model callers; pass --confirm to proceed")
				}
				if err := requireID(args[0], "deployment ID"); err != nil {
					return err
				}
				if err := requireVersion(revision, "--revision"); err != nil {
					return err
				}
				if err := requireID(key, "--idempotency-key"); err != nil {
					return err
				}
				why, err := requiredText(reason, "--reason", 500)
				if err != nil {
					return err
				}
				return opts.actOnDeployment(cmd, *workspace, args[0], key, deploymentAction{action.action, revision, why})
			},
		}
		cmd.Flags().Int64Var(&revision, "revision", 0, "Current revision from deployment get --output json")
		cmd.Flags().StringVar(&reason, "reason", "", "Reason for the audit record (required)")
		cmd.Flags().StringVar(&key, "idempotency-key", "", "Request UUID (required, reuse the original key on retry)")
		if action.confirm {
			cmd.Flags().BoolVar(&confirm, "confirm", false, "Confirm that the action can interrupt callers")
			cmd.Example += " --confirm"
		}
		group.AddCommand(cmd)
	}
}

func (opts *options) createDeploymentCommand(workspace *string) *cobra.Command {
	var input deploymentCreate
	var key string
	cmd := &cobra.Command{
		Use:     "create",
		Short:   "Request a new deployment on a selected node and GPU.",
		Example: "  ownsolid deployment create --model \"$MODEL_ID\" --node \"$NODE_ID\" --gpu \"$GPU_UUID\" --idempotency-key \"$REQUEST_KEY\" --reason 'Publish approved model'",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			for _, flag := range []struct{ value, name string }{
				{input.ModelID, "--model"}, {input.NodeID, "--node"}, {key, "--idempotency-key"},
			} {
				if err := requireID(flag.value, flag.name); err != nil {
					return err
				}
			}
			gpu, valid := strings.CutPrefix(input.GPUUUID, "GPU-")
			if !valid || !resourceID.MatchString(gpu) {
				return errors.New("--gpu must be a GPU UUID with the GPU- prefix; see ownsolid deployment placement")
			}
			why, err := requiredText(input.Reason, "--reason", 500)
			if err != nil {
				return err
			}
			payload := input
			payload.Reason = why
			return opts.createDeployment(cmd, *workspace, key, payload)
		},
	}
	cmd.Flags().StringVar(&input.ModelID, "model", "", "Approved model UUID (required)")
	cmd.Flags().StringVar(&input.NodeID, "node", "", "Selected node UUID (required)")
	cmd.Flags().StringVar(&input.GPUUUID, "gpu", "", "GPU UUID from deployment placement (required)")
	cmd.Flags().StringVar(&input.Reason, "reason", "", "Reason for the audit record (required)")
	cmd.Flags().StringVar(&key, "idempotency-key", "", "Request UUID (required, reuse the original key on retry)")
	return cmd
}

func (opts *options) createDeployment(cmd *cobra.Command, workspace, key string, input deploymentCreate) error {
	cfg, client, err := opts.operator(cmd, workspace)
	if err != nil {
		return err
	}
	defer client.http.CloseIdleConnections()
	ctx := cmd.Context()
	workspace, err = client.workspace(ctx, cfg, workspace)
	if err != nil {
		return err
	}
	if _, err := client.detail(ctx, cfg.adminPath(nodes), input.NodeID, workspace); err != nil {
		return err
	}
	req := request{method: http.MethodPost, path: cfg.adminPath(deployments), status: http.StatusAccepted, key: key}
	return opts.deploymentMutation(cmd, client, req, input, "", workspace)
}

func (opts *options) actOnDeployment(cmd *cobra.Command, workspace, id, key string, input deploymentAction) error {
	cfg, client, err := opts.operator(cmd, workspace)
	if err != nil {
		return err
	}
	defer client.http.CloseIdleConnections()
	if workspace == "" {
		workspace = cfg.WorkspaceID
	}
	path := cfg.adminPath(deployments)
	if workspace != "" {
		if _, err := client.detail(cmd.Context(), path, id, workspace); err != nil {
			return err
		}
	}
	req := request{method: http.MethodPost, path: path + "/" + id + "/actions", status: http.StatusAccepted, key: key}
	return opts.deploymentMutation(cmd, client, req, input, id, workspace)
}

func (opts *options) deploymentMutation(cmd *cobra.Command, client *apiClient, req request, input any, id, workspace string) error {
	var result json.RawMessage
	if err := client.mutation(cmd.Context(), req, input, &result); err != nil {
		return err
	}
	if err := validateScope(result, id, workspace); err != nil {
		return err
	}
	columns := append(slices.Clone(deployments.columns), column{"REVISION", "revision"}, column{"TASK STATE", "task_state"})
	return opts.renderItem(cmd, columns, result)
}

func (opts *options) placementCommand(workspace *string) *cobra.Command {
	return &cobra.Command{
		Use:     "placement <model-id>",
		Short:   "Show the eligible GPUs and recommended placement for an approved model.",
		Example: "  ownsolid deployment placement \"$MODEL_ID\"",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireID(args[0], "model ID"); err != nil {
				return err
			}
			return opts.readPlacement(cmd, *workspace, args[0])
		},
	}
}

func (opts *options) readPlacement(cmd *cobra.Command, workspace, model string) error {
	cfg, client, err := opts.operator(cmd, workspace)
	if err != nil {
		return err
	}
	defer client.http.CloseIdleConnections()
	ctx := cmd.Context()
	workspace, err = client.workspace(ctx, cfg, workspace)
	if err != nil {
		return err
	}
	var result json.RawMessage
	if err := client.get(ctx, cfg.adminPath(deployments)+"/placement", url.Values{"model_id": {model}}, &result); err != nil {
		return err
	}
	var placement struct {
		Model     string `json:"model_id"`
		Workspace string `json:"workspace_id"`
		Best      *struct {
			Node string `json:"node_id"`
			GPU  string `json:"gpu_uuid"`
		} `json:"recommended"`
		Candidates []struct {
			ID      string `json:"node_id"`
			Name    string `json:"name"`
			Refusal string `json:"refusal"`
			GPUs    []struct {
				ID      string `json:"uuid"`
				FreeMiB int64  `json:"memory_free_mib"`
				Refusal string `json:"refusal"`
			} `json:"gpus"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(result, &placement); err != nil || placement.Model != model || placement.Candidates == nil {
		return errors.New("the server returned an invalid deployment placement")
	}
	if placement.Workspace != workspace {
		return errors.New("the model is outside the selected workspace; check --workspace or workspace_id in config.json")
	}
	var rows []json.RawMessage
	if opts.output == "table" {
		for _, node := range placement.Candidates {
			for _, gpu := range node.GPUs {
				refusal := gpu.Refusal
				if node.Refusal != "" {
					refusal = node.Refusal
				}
				row, err := json.Marshal(struct {
					NodeID      string `json:"node_id"`
					Name        string `json:"name"`
					GPU         string `json:"gpu"`
					FreeMiB     int64  `json:"free_mib"`
					Refusal     string `json:"refusal,omitempty"`
					Recommended bool   `json:"recommended"`
				}{node.ID, node.Name, gpu.ID, gpu.FreeMiB, refusal, placement.Best != nil && placement.Best.Node == node.ID && placement.Best.GPU == gpu.ID})
				if err != nil {
					return err
				}
				rows = append(rows, row)
			}
		}
	}
	columns := []column{{"NODE", "name"}, {"NODE ID", "node_id"}, {"GPU UUID", "gpu"}, {"FREE MiB", "free_mib"}, {"RECOMMENDED", "recommended"}, {"REFUSAL", "refusal"}}
	return render(cmd.OutOrStdout(), opts.output, columns, result, rows)
}
