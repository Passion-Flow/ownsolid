package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

var trainingJobs = resource{"training/jobs", "training", []column{
	{"ID", "id"}, {"STATE", "state"}, {"REVISION", "revision"},
	{"NODE ID", "current_node_id"}, {"WAIT CODE", "wait_code"},
}}

type trainingCreate struct {
	WorkspaceID      string          `json:"workspace_id"`
	NodeID           string          `json:"node_id"`
	ModelID          string          `json:"model_id"`
	DatasetID        string          `json:"dataset_id"`
	DatasetVersionID string          `json:"dataset_version_id"`
	GPUUUID          string          `json:"gpu_uuid"`
	Configuration    json.RawMessage `json:"configuration"`
	Reason           string          `json:"reason"`
}

type trainingCancellation struct {
	Revision int64  `json:"expected_revision"`
	Reason   string `json:"reason"`
}

func (opts *options) trainingCommand() *cobra.Command {
	var workspace string
	group := &cobra.Command{
		Use:     "training",
		Short:   "Manage training jobs and read attempt history and metrics.",
		Example: "  ownsolid training get \"$JOB_ID\" --output json",
	}
	group.PersistentFlags().StringVar(&workspace, "workspace", "", "Workspace UUID (defaults to the configured or current workspace)")
	group.AddCommand(
		opts.createTrainingJob(&workspace), opts.getTrainingJob(&workspace), opts.cancelTrainingJob(&workspace),
		opts.listTrainingAttempts(&workspace), opts.listTrainingMetrics(&workspace),
	)
	return group
}

func (opts *options) createTrainingJob(workspace *string) *cobra.Command {
	var input trainingCreate
	var configuration, key, reason string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Queue a training job using a retained dataset version and a local JSON configuration.",
		Example: "  ownsolid training create --node \"$NODE_ID\" --gpu \"$GPU_UUID\" --model \"$MODEL_ID\" " +
			"--dataset \"$DATASET_ID\" --dataset-version \"$VERSION_ID\" --configuration training.json " +
			"--idempotency-key \"$REQUEST_KEY\" --reason 'Train approved adapter'",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			for _, flag := range []struct{ value, name string }{
				{input.NodeID, "--node"}, {input.ModelID, "--model"}, {input.DatasetID, "--dataset"},
				{input.DatasetVersionID, "--dataset-version"}, {key, "--idempotency-key"},
			} {
				if err := requireID(flag.value, flag.name); err != nil {
					return err
				}
			}
			gpu, prefixed := strings.CutPrefix(input.GPUUUID, "GPU-")
			if !prefixed || !resourceID.MatchString(gpu) {
				return errors.New("--gpu must be a UUID with the GPU- prefix")
			}
			why, err := requiredText(reason, "--reason", 500)
			if err != nil {
				return err
			}
			config, err := readTrainingConfiguration(configuration)
			if err != nil {
				return err
			}
			path, scope, client, err := opts.trainingClient(cmd, *workspace)
			if err != nil {
				return err
			}
			defer client.http.CloseIdleConnections()
			payload := input
			payload.WorkspaceID, payload.Configuration, payload.Reason = scope, config, why
			req := request{method: http.MethodPost, path: path, status: http.StatusCreated, key: key}
			return opts.trainingMutation(cmd, client, req, payload, "", scope)
		},
	}
	cmd.Flags().StringVar(&input.NodeID, "node", "", "Selected node UUID")
	cmd.Flags().StringVar(&input.GPUUUID, "gpu", "", "Selected GPU UUID with the GPU- prefix")
	cmd.Flags().StringVar(&input.ModelID, "model", "", "Approved model UUID")
	cmd.Flags().StringVar(&input.DatasetID, "dataset", "", "Dataset UUID")
	cmd.Flags().StringVar(&input.DatasetVersionID, "dataset-version", "", "Published dataset version UUID")
	cmd.Flags().StringVar(&configuration, "configuration", "", "Path to a local training configuration JSON file")
	cmd.Flags().StringVar(&key, "idempotency-key", "", "Request UUID (reuse the original key on retry)")
	cmd.Flags().StringVar(&reason, "reason", "", "Reason for the audit record")
	return cmd
}

func (opts *options) getTrainingJob(workspace *string) *cobra.Command {
	return &cobra.Command{
		Use:     "get <job-id>",
		Short:   "Show a training job's current state and revision.",
		Example: "  ownsolid training get \"$JOB_ID\" --output yaml",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if err := requireID(id, "training job ID"); err != nil {
				return err
			}
			path, scope, client, err := opts.trainingClient(cmd, *workspace)
			if err != nil {
				return err
			}
			defer client.http.CloseIdleConnections()
			var result json.RawMessage
			if err := client.get(cmd.Context(), path+"/"+id, url.Values{"workspace_id": {scope}}, &result); err != nil {
				return err
			}
			if err := validateScope(result, id, scope); err != nil {
				return err
			}
			return opts.renderItem(cmd, trainingJobs.columns, result)
		},
	}
}

func (opts *options) cancelTrainingJob(workspace *string) *cobra.Command {
	var revision int64
	var reason string
	cmd := &cobra.Command{
		Use:     "cancel <job-id>",
		Short:   "Request cancellation of the current training job revision.",
		Example: "  ownsolid training cancel \"$JOB_ID\" --revision \"$REVISION\" --reason 'Training withdrawn'",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if err := requireID(id, "training job ID"); err != nil {
				return err
			}
			if err := requireVersion(revision, "--revision"); err != nil {
				return err
			}
			why, err := requiredText(reason, "--reason", 500)
			if err != nil {
				return err
			}
			path, scope, client, err := opts.trainingClient(cmd, *workspace)
			if err != nil {
				return err
			}
			defer client.http.CloseIdleConnections()
			req := request{
				method: http.MethodPost, path: path + "/" + id + "/cancellations",
				query: url.Values{"workspace_id": {scope}}, status: http.StatusOK,
			}
			return opts.trainingMutation(cmd, client, req, trainingCancellation{revision, why}, id, scope)
		},
	}
	cmd.Flags().Int64Var(&revision, "revision", 0, "Current revision from training get --output json")
	cmd.Flags().StringVar(&reason, "reason", "", "Reason for the audit record")
	return cmd
}

func (opts *options) trainingClient(cmd *cobra.Command, workspace string) (string, string, *apiClient, error) {
	cfg, client, err := opts.operator(cmd, workspace)
	if err != nil {
		return "", "", nil, err
	}
	scope, err := client.workspace(cmd.Context(), cfg, workspace)
	if err != nil {
		client.http.CloseIdleConnections()
		return "", "", nil, err
	}
	return cfg.adminPath(trainingJobs), scope, client, nil
}

func (opts *options) trainingMutation(cmd *cobra.Command, client *apiClient, req request, payload any, id, scope string) error {
	var result json.RawMessage
	if err := client.mutation(cmd.Context(), req, payload, &result); err != nil {
		return err
	}
	if err := validateScope(result, id, scope); err != nil {
		return err
	}
	return opts.renderItem(cmd, trainingJobs.columns, result)
}

func readTrainingConfiguration(path string) (json.RawMessage, error) {
	if path == "" {
		return nil, errors.New("supply a training configuration file with --configuration")
	}
	file, size, err := openRegularInput(path, "training configuration")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	const maxConfigurationBytes = 64 << 10
	if size > maxConfigurationBytes {
		return nil, errors.New("training configuration exceeds 64 KiB")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxConfigurationBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read training configuration: %w", err)
	}
	if len(raw) > maxConfigurationBytes {
		return nil, errors.New("training configuration exceeds 64 KiB")
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' || !utf8.Valid(raw) || !json.Valid(raw) {
		return nil, errors.New("training configuration must contain one JSON object")
	}
	return json.RawMessage(raw), nil
}
