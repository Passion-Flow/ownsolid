package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/spf13/cobra"
)

var agentPacing = resource{key: "agent-pacing", columns: []column{
	{"HEARTBEAT (S)", "heartbeat_seconds"}, {"JITTER (%)", "jitter_percent"},
	{"RECONNECT BASE (S)", "reconnect_base_seconds"}, {"RECONNECT MAX (S)", "reconnect_max_seconds"},
	{"ONLINE WINDOW (S)", "online_window_seconds"}, {"VERSION", "version"}, {"UPDATED", "updated_at"},
}}

type pacingUpdate struct {
	HeartbeatSeconds     int    `json:"heartbeat_seconds"`
	JitterPercent        int    `json:"jitter_percent"`
	ReconnectBaseSeconds int    `json:"reconnect_base_seconds"`
	ReconnectMaxSeconds  int    `json:"reconnect_max_seconds"`
	Version              int64  `json:"version"`
	Reason               string `json:"reason"`
}

func (opts *options) agentPacingCommand() *cobra.Command {
	group := &cobra.Command{
		Use:     "pacing",
		Short:   "Read or change agent heartbeat and reconnect pacing in the configured cell.",
		Example: "  ownsolid node pacing get --output json",
	}
	group.AddCommand(&cobra.Command{
		Use:     "get",
		Short:   "Read the stored pacing and online window.",
		Example: "  ownsolid node pacing get --output json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return opts.runAgentPacing(cmd, nil)
		},
	}, opts.setAgentPacingCommand())
	return group
}

func (opts *options) setAgentPacingCommand() *cobra.Command {
	var input pacingUpdate
	var confirm bool
	fields := []struct {
		flag, help string
		value      *int
	}{
		{"heartbeat-seconds", "Mean seconds between heartbeats", &input.HeartbeatSeconds},
		{"jitter-percent", "Percentage of heartbeat variation", &input.JitterPercent},
		{"reconnect-base-seconds", "Reconnect ceiling in seconds after the first failure", &input.ReconnectBaseSeconds},
		{"reconnect-max-seconds", "Maximum reconnect ceiling in seconds", &input.ReconnectMaxSeconds},
	}
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Replace the configured cell's pacing at the current version.",
		Example: "  ownsolid node pacing set --heartbeat-seconds 15 --jitter-percent 10 --reconnect-base-seconds 2 " +
			"--reconnect-max-seconds 120 --version \"$VERSION\" --reason 'Reduce maintenance load' --confirm",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !confirm {
				return errors.New("pacing affects agents throughout the configured cell; pass --confirm to proceed")
			}
			for _, field := range fields {
				if !cmd.Flags().Changed(field.flag) {
					return fmt.Errorf("--%s is required", field.flag)
				}
			}
			if err := requireVersion(input.Version, "--version"); err != nil {
				return err
			}
			reason, err := requiredText(input.Reason, "--reason", 500)
			if err != nil {
				return err
			}
			payload := input
			payload.Reason = reason
			return opts.runAgentPacing(cmd, &payload)
		},
	}
	for _, field := range fields {
		cmd.Flags().IntVar(field.value, field.flag, 0, field.help+" (required)")
	}
	cmd.Flags().Int64Var(&input.Version, "version", 0, "Version from node pacing get --output json (required)")
	cmd.Flags().StringVar(&input.Reason, "reason", "", "Reason for the audit record (required)")
	cmd.Flags().BoolVar(&confirm, "confirm", false, "Confirm replacing pacing for the configured cell")
	return cmd
}

func (opts *options) runAgentPacing(cmd *cobra.Command, input *pacingUpdate) error {
	cfg, client, err := opts.operator(cmd, "")
	if err != nil {
		return err
	}
	defer client.http.CloseIdleConnections()
	path := cfg.adminPath(agentPacing)
	var result json.RawMessage
	if input == nil {
		err = client.get(cmd.Context(), path, nil, &result)
	} else {
		req := request{method: http.MethodPut, path: path, status: http.StatusOK}
		err = client.mutation(cmd.Context(), req, *input, &result)
	}
	if err != nil {
		return err
	}
	return opts.renderItem(cmd, agentPacing.columns, result)
}
