package cli

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

const maximumTrainingMetricCursorBytes = 2048

var trainingAttemptColumns = []column{
	{"ATTEMPT", "attempt_number"}, {"ID", "id"}, {"STATE", "state"},
	{"NODE ID", "node_id"}, {"STEP", "last_global_step"}, {"ERROR", "error_code"},
}

var trainingCheckpointColumns = []column{
	{"ID", "id"}, {"ATTEMPT ID", "attempt_id"}, {"STEP", "global_step"},
	{"RECEIPT SHA256", "receipt_sha256"}, {"CREATED", "created_at"},
}

var trainingRecoveryColumns = []column{
	{"ID", "id"}, {"MODE", "mode"}, {"PREVIOUS ATTEMPT", "previous_attempt_id"},
	{"NEXT ATTEMPT", "next_attempt_id"}, {"CHECKPOINT ID", "checkpoint_id"}, {"CREATED", "created_at"},
}

var trainingMetricColumns = []column{
	{"SEQUENCE", "sequence"}, {"STEP", "global_step"}, {"RECORDED", "recorded_at"}, {"VALUES", "values"},
}

func (opts *options) listTrainingJobHistory(workspace *string, collection, record string, columns []column,
	valid func(json.RawMessage, string) bool,
) *cobra.Command {
	var paging pageOptions
	cmd := &cobra.Command{
		Use:     collection + " <job-id>",
		Short:   fmt.Sprintf("Read one page of a training job's %s history.", record),
		Example: fmt.Sprintf("  ownsolid training %s \"$JOB_ID\" --limit 10", collection),
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			job := args[0]
			page, err := opts.readTrainingPage(cmd, *workspace, job, collection, "", paging)
			if err != nil {
				return err
			}
			for _, item := range page.Items {
				if !valid(item, job) {
					return fmt.Errorf("the server returned an invalid %s or a different training job", record)
				}
			}
			return opts.renderPage(cmd, columns, page)
		},
	}
	paging.bind(cmd, "Next cursor from the same collection, workspace and training job")
	return cmd
}

func validTrainingAttempt(raw json.RawMessage, job string) bool {
	var attempt struct {
		ID    string `json:"id"`
		JobID string `json:"job_id"`
	}
	return json.Unmarshal(raw, &attempt) == nil && resourceID.MatchString(attempt.ID) && attempt.JobID == job
}

func validTrainingCheckpoint(raw json.RawMessage, job string) bool {
	var checkpoint struct {
		ID        string `json:"id"`
		JobID     string `json:"job_id"`
		AttemptID string `json:"attempt_id"`
	}
	return json.Unmarshal(raw, &checkpoint) == nil && resourceID.MatchString(checkpoint.ID) &&
		checkpoint.JobID == job && resourceID.MatchString(checkpoint.AttemptID)
}

func validTrainingRecovery(raw json.RawMessage, job string) bool {
	var recovery struct {
		ID                string  `json:"id"`
		JobID             string  `json:"job_id"`
		PreviousAttemptID *string `json:"previous_attempt_id"`
		NextAttemptID     *string `json:"next_attempt_id"`
		CheckpointID      *string `json:"checkpoint_id"`
		Mode              string  `json:"mode"`
	}
	if json.Unmarshal(raw, &recovery) != nil || !resourceID.MatchString(recovery.ID) || recovery.JobID != job {
		return false
	}
	for _, id := range []*string{recovery.PreviousAttemptID, recovery.NextAttemptID, recovery.CheckpointID} {
		if id != nil && !resourceID.MatchString(*id) {
			return false
		}
	}
	return recovery.Mode == "checkpoint" && recovery.CheckpointID != nil ||
		recovery.Mode == "restart" && recovery.CheckpointID == nil
}

func (opts *options) listTrainingMetrics(workspace *string) *cobra.Command {
	paging := pageOptions{cursorBytes: maximumTrainingMetricCursorBytes}
	var attemptID string
	cmd := &cobra.Command{
		Use:     "metrics <job-id>",
		Short:   "Read one page of metrics for a training attempt.",
		Example: "  ownsolid training metrics \"$JOB_ID\" --attempt \"$ATTEMPT_ID\" --limit 20",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireID(attemptID, "--attempt"); err != nil {
				return err
			}
			page, err := opts.readTrainingPage(cmd, *workspace, args[0], "metrics", attemptID, paging)
			if err != nil {
				return err
			}
			var previousSequence int64
			for _, item := range page.Items {
				var metric struct {
					AttemptID string `json:"attempt_id"`
					Sequence  int64  `json:"sequence"`
				}
				if err := json.Unmarshal(item, &metric); err != nil ||
					metric.AttemptID != attemptID || metric.Sequence <= previousSequence {
					return errors.New("the server returned an invalid metric sequence or a different training attempt")
				}
				previousSequence = metric.Sequence
			}
			return opts.renderPage(cmd, trainingMetricColumns, page)
		},
	}
	cmd.Flags().StringVar(&attemptID, "attempt", "", "Attempt UUID from training attempts (required)")
	paging.bind(cmd, "Next cursor from the same workspace, training job and attempt")
	return cmd
}

func (opts *options) readTrainingPage(cmd *cobra.Command, workspace, job, collection, attempt string, paging pageOptions) (boundedPage, error) {
	if err := requireID(job, "training job ID"); err != nil {
		return boundedPage{}, err
	}
	query, err := paging.query(cmd)
	if err != nil {
		return boundedPage{}, err
	}
	path, scope, client, err := opts.trainingClient(cmd, workspace)
	if err != nil {
		return boundedPage{}, err
	}
	defer client.http.CloseIdleConnections()
	query.Set("workspace_id", scope)
	if attempt != "" {
		query.Set("attempt_id", attempt)
	}
	var page boundedPage
	if err := client.get(cmd.Context(), path+"/"+job+"/"+collection, query, &page); err != nil {
		return boundedPage{}, err
	}
	if err := paging.validate(page, "training "+collection); err != nil {
		return boundedPage{}, err
	}
	return page, nil
}
