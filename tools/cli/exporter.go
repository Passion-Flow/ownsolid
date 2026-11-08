package cli

import (
	"encoding/json"
	"errors"

	"github.com/spf13/cobra"
)

const exporterPath = "/components/node-exporter"

type exporterSettings struct {
	LogLevel          string `json:"log_level"`
	MaxScrapeRequests int    `json:"max_scrape_requests"`
}

type exporterAction struct {
	Action  string            `json:"action"`
	Reason  string            `json:"reason"`
	Version string            `json:"version,omitempty"`
	Config  *exporterSettings `json:"config,omitempty"`
}

func (opts *options) exporterCommand() *cobra.Command {
	group := &cobra.Command{
		Use:     "exporter",
		Short:   "Manage the approved node exporter through the node agent.",
		Example: "  ownsolid node exporter status \"$NODE_ID\"",
	}
	group.AddCommand(opts.exporterReadCommand(false), opts.exporterReadCommand(true))
	for _, action := range []struct {
		command, action, description string
	}{
		{"install", "install", "Request installation of an approved node exporter."},
		{"start", "start", "Request that the managed node exporter start."},
		{"stop", "stop", "Request that the managed node exporter stop."},
		{"remove", "remove", "Request removal of the owned node exporter installation."},
		{"observe", "observe", "Request a fresh node exporter observation."},
		{"configure", "configure", "Apply a log level and scrape concurrency to the node exporter."},
		{"restart", "restart", "Request that the managed node exporter restart."},
		{"upgrade", "upgrade", "Request an upgrade to a newer approved node exporter version."},
		{"rollback", "rollback", "Request rollback to the node exporter's previous approved version."},
		{"collect-logs", "logs", "Request a bounded snapshot of the node exporter's systemd journal."},
	} {
		group.AddCommand(opts.exporterActionCommand(action.command, action.action, action.description))
	}
	return group
}

func (opts *options) exporterActionCommand(command, action, description string) *cobra.Command {
	var key, reason, version string
	var configuration exporterSettings
	var confirm bool
	cmd := &cobra.Command{
		Use:     command + " <name-or-id>",
		Short:   description,
		Example: "  ownsolid node exporter " + command + " \"$NODE_ID\" --idempotency-key \"$REQUEST_KEY\" --reason 'Node monitoring maintenance'",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if action == "remove" && !confirm {
				return errors.New("removal deletes the managed node exporter installation; pass --confirm to proceed")
			}
			if action == "upgrade" && version == "" {
				return errors.New("upgrade requires --version from ownsolid node exporter status --output json")
			}
			why, err := requiredText(reason, "--reason", 500)
			if err != nil {
				return err
			}
			input := exporterAction{Action: action, Reason: why, Version: version}
			if action == "configure" || cmd.Flags().Changed("log-level") || cmd.Flags().Changed("max-scrape-requests") {
				if !cmd.Flags().Changed("log-level") || !cmd.Flags().Changed("max-scrape-requests") {
					return errors.New("supply --log-level and --max-scrape-requests together")
				}
				switch configuration.LogLevel {
				case "debug", "info", "warn", "error":
				default:
					return errors.New("--log-level must be debug, info, warn, or error")
				}
				if configuration.MaxScrapeRequests < 1 || configuration.MaxScrapeRequests > 40 {
					return errors.New("--max-scrape-requests must be between 1 and 40")
				}
				input.Config = &configuration
			}
			return opts.submitNodeTask(cmd, args[0], key, exporterPath+"/actions", input)
		},
	}
	cmd.Flags().StringVar(&key, "idempotency-key", "", "Request UUID (required, reuse the original key on retry)")
	cmd.Flags().StringVar(&reason, "reason", "", "Reason for the audit record (required)")
	switch action {
	case "install", "configure", "upgrade", "rollback":
		cmd.Flags().StringVar(&version, "version", "", "Approved version from exporter status (required for upgrade)")
	}
	if action == "install" || action == "configure" {
		cmd.Flags().StringVar(&configuration.LogLevel, "log-level", "", "Exporter log level: debug, info, warn, or error")
		cmd.Flags().IntVar(&configuration.MaxScrapeRequests, "max-scrape-requests", 0, "Concurrent scrape limit from 1 to 40")
	}
	switch action {
	case "upgrade":
		cmd.Example += " --version \"$APPROVED_VERSION\""
	case "configure":
		cmd.Example += " --log-level warn --max-scrape-requests 4"
	case "remove":
		cmd.Flags().BoolVar(&confirm, "confirm", false, "Confirm removal of the managed installation")
		cmd.Example += " --confirm"
	}
	return cmd
}

func (opts *options) exporterReadCommand(logs bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "status <name-or-id>",
		Short:   "Read desired state, observations and available node exporter versions.",
		Example: "  ownsolid node exporter status \"$NODE_ID\" --output json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, client, err := opts.operator(cmd, "")
			if err != nil {
				return err
			}
			defer client.http.CloseIdleConnections()
			ctx := cmd.Context()
			id, err := client.nodeTarget(ctx, cfg, args[0])
			if err != nil {
				return err
			}
			path := cfg.adminPath(nodes) + "/" + id + exporterPath
			if logs {
				path += "/logs"
			}
			var raw json.RawMessage
			if err := client.get(ctx, path, nil, &raw); err != nil {
				return err
			}
			return opts.renderExporter(cmd, raw, id, logs)
		},
	}
	if logs {
		cmd.Use = "logs <name-or-id>"
		cmd.Short = "Read the latest collected node exporter journal snapshot."
		cmd.Example = "  ownsolid node exporter logs \"$NODE_ID\" --output yaml"
	}
	return cmd
}

func (opts *options) renderExporter(cmd *cobra.Command, raw json.RawMessage, id string, logs bool) error {
	var view struct {
		NodeID      string `json:"node_id"`
		Observation *struct {
			State string `json:"state"`
		} `json:"observation"`
		Snapshot *struct {
			Entries []json.RawMessage `json:"entries"`
		} `json:"snapshot"`
	}
	if err := json.Unmarshal(raw, &view); err != nil || view.NodeID != id {
		return errors.New("the server returned a node exporter result for a different or invalid node")
	}
	if logs {
		var rows []json.RawMessage
		if view.Snapshot != nil {
			rows = view.Snapshot.Entries
		}
		columns := []column{{"AT", "timestamp"}, {"PRIORITY", "priority"}, {"MESSAGE", "message"}}
		return render(cmd.OutOrStdout(), opts.output, columns, raw, rows)
	}
	var row = raw
	if opts.output == "table" && view.Observation != nil {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		observed, err := json.Marshal(view.Observation.State)
		if err != nil {
			return err
		}
		fields["observed_state"] = observed
		row, err = json.Marshal(fields)
		if err != nil {
			return err
		}
	}
	columns := []column{{"NODE ID", "node_id"}, {"VERSION", "version"}, {"DESIRED", "desired_state"}, {"OBSERVED", "observed_state"},
		{"STALE", "stale"}, {"ASSET AVAILABLE", "artifact_available"}, {"LAST TASK", "last_task_id"}}
	return render(cmd.OutOrStdout(), opts.output, columns, raw, []json.RawMessage{row})
}
