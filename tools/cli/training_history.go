package cli

import (
	"encoding/json"
	"errors"

	"github.com/spf13/cobra"
)

const maximumTrainingMetricCursorBytes = 2048

var trainingAttemptColumns = []column{
	{"ATTEMPT", "attempt_number"}, {"ID", "id"}, {"STATE", "state"},
	{"NODE ID", "node_id"}, {"STEP", "last_global_step"}, {"ERROR", "error_code"},
}

var trainingMetricColumns = []column{
	{"SEQUENCE", "sequence"}, {"STEP", "global_step"}, {"RECORDED", "recorded_at"}, {"VALUES", "values"},
}

func (opts *options) listTrainingAttempts(workspace *string) *cobra.Command {
	var paging pageOptions
	cmd := &cobra.Command{
		Use:     "attempts <job-id>",
		Short:   "Read one page of a training job's attempt history.",
		Example: "  ownsolid training attempts \"$JOB_ID\" --limit 10",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			job := args[0]
			page, err := opts.readTrainingPage(cmd, *workspace, job, "attempts", "", paging)
			if err != nil {
				return err
			}
			for _, item := range page.Items {
				var attempt struct {
					ID    string `json:"id"`
					JobID string `json:"job_id"`
				}
				if err := json.Unmarshal(item, &attempt); err != nil ||
					!resourceID.MatchString(attempt.ID) || attempt.JobID != job {
					return errors.New("the server returned an invalid attempt or a different training job")
				}
			}
			return opts.renderPage(cmd, trainingAttemptColumns, page)
		},
	}
	paging.bind(cmd, "Next cursor from the same workspace and training job")
	return cmd
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
