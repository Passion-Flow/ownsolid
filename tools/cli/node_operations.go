package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"github.com/spf13/cobra"
)

type nodeUpdate struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Version int64  `json:"version"`
	Reason  string `json:"reason"`
}

func (opts *options) addNodeOperations(group *cobra.Command) {
	group.AddCommand(opts.nodeTasksCommand(), opts.nodeTaskCommand(), opts.nodeTaskLogCommand(), opts.collectFactsCommand(),
		opts.joinTokensCommand(), opts.nodeInferenceCommand(), opts.probeInferenceCommand())
	for _, change := range []struct {
		command, status, description string
	}{
		{"disable", "disabled", "Disable a node for maintenance."},
		{"enable", "active", "Return a disabled node to service."},
		{"rename", "", "Change a node's display name."},
		{"remove", "removed", "Remove a node and revoke its agent credential."},
	} {
		var version int64
		var reason string
		var confirm bool
		use, example, args := change.command+" <name-or-id>", "gpu-worker-01", cobra.ExactArgs(1)
		if change.command == "rename" {
			use += " <new-name>"
			example += " gpu-worker-02"
			args = cobra.ExactArgs(2)
		}
		example = "  ownsolid node " + change.command + " " + example + " --version 1 --reason 'Scheduled maintenance'"
		cmd := &cobra.Command{
			Use: use, Short: change.description, Example: example, Args: args,
			RunE: func(cmd *cobra.Command, args []string) error {
				if change.command == "remove" && !confirm {
					return errors.New("removal revokes the agent credential; pass --confirm to proceed")
				}
				if err := requireVersion(version, "--version"); err != nil {
					return err
				}
				why, err := requiredText(reason, "--reason", 500)
				if err != nil {
					return err
				}
				input := nodeUpdate{Status: change.status, Version: version, Reason: why}
				if change.command == "rename" {
					input.Name, err = requiredText(args[1], "new name", 100)
					if err != nil {
						return err
					}
				}
				return opts.updateNode(cmd, args[0], input)
			},
		}
		cmd.Flags().Int64Var(&version, "version", 0, "Current version from node get --output json")
		cmd.Flags().StringVar(&reason, "reason", "", "Reason for the audit record (required)")
		if change.command == "remove" {
			cmd.Flags().BoolVar(&confirm, "confirm", false, "Confirm permanent agent credential revocation")
			cmd.Example += " --confirm"
		}
		group.AddCommand(cmd)
	}
}

func (opts *options) updateNode(cmd *cobra.Command, name string, input nodeUpdate) error {
	cfg, client, err := opts.operator(cmd, "")
	if err != nil {
		return err
	}
	defer client.http.CloseIdleConnections()
	ctx, path := cmd.Context(), cfg.adminPath(nodes)
	id, err := client.nodeTarget(ctx, cfg, name)
	if err != nil {
		return err
	}
	item, err := client.detail(ctx, path, id, "")
	if err != nil {
		return err
	}
	var current nodeUpdate
	if err := json.Unmarshal(item, &current); err != nil || current.Name == "" || current.Status == "" || current.Version < 1 {
		return errors.New("the server returned a node without a valid name, status, and version")
	}
	if current.Version != input.Version {
		return errors.New("VERSION_CONFLICT: the node has changed; read it again and review its current version before submitting a new update")
	}
	if input.Name == "" {
		input.Name = current.Name
	}
	if input.Status == "" {
		input.Status = current.Status
	}
	var updated json.RawMessage
	if err := client.mutation(ctx, request{method: http.MethodPatch, path: path + "/" + id, status: http.StatusOK}, input, &updated); err != nil {
		return err
	}
	if err := validateScope(updated, id, ""); err != nil {
		return err
	}
	columns := append(slices.Clone(nodes.columns), column{"VERSION", "version"})
	return opts.renderItem(cmd, columns, updated)
}

func (c *apiClient) nodeTarget(ctx context.Context, cfg settings, name string) (string, error) {
	if name == "" {
		return "", errors.New("supply a node name or UUID")
	}
	if id := lookupID(name); id != "" {
		return id, nil
	}
	return c.resolveNode(ctx, cfg, name)
}

var nodeTasks = resource{key: "tasks", columns: []column{
	{"ID", "id"}, {"NODE ID", "node_id"}, {"OPERATION", "operation"}, {"STATE", "state"}, {"ERROR", "error_code"},
}}

func (opts *options) nodeTasksCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "tasks <name-or-id>",
		Short:   "List tasks issued to a node.",
		Example: "  ownsolid node tasks gpu-worker-01 --output json",
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
			items, err := client.list(ctx, cfg.adminPath(nodes)+"/"+id+"/tasks", make(url.Values), true)
			if err != nil {
				return err
			}
			return render(cmd.OutOrStdout(), opts.output, nodeTasks.columns, items, items)
		},
	}
}

func (opts *options) nodeTaskCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "task <task-id>",
		Short:   "Read a node task and its last known state.",
		Example: "  ownsolid node task \"$TASK_ID\" --output yaml",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireID(args[0], "task ID"); err != nil {
				return err
			}
			cfg, client, err := opts.operator(cmd, "")
			if err != nil {
				return err
			}
			defer client.http.CloseIdleConnections()
			var result json.RawMessage
			if err := client.get(cmd.Context(), cfg.adminPath(nodeTasks)+"/"+args[0], nil, &result); err != nil {
				return err
			}
			var detail struct {
				Task json.RawMessage `json:"task"`
			}
			if err := json.Unmarshal(result, &detail); err != nil {
				return errors.New("the server returned an invalid task detail")
			}
			if err := validateScope(detail.Task, args[0], ""); err != nil {
				return err
			}
			return render(cmd.OutOrStdout(), opts.output, nodeTasks.columns, result, []json.RawMessage{detail.Task})
		},
	}
}

func (opts *options) nodeTaskLogCommand() *cobra.Command {
	const maxPage = 500
	var after string
	var limit int
	cmd := &cobra.Command{
		Use:     "task-log <task-id>",
		Short:   "Read a bounded page of progress logs for a node task.",
		Example: "  ownsolid node task-log \"$TASK_ID\" --output json\n  ownsolid node task-log \"$TASK_ID\" --after \"$CURSOR\"",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireID(args[0], "task ID"); err != nil {
				return err
			}
			if limit < 1 || limit > maxPage {
				return errors.New("--limit must be between 1 and 500")
			}
			cfg, client, err := opts.operator(cmd, "")
			if err != nil {
				return err
			}
			defer client.http.CloseIdleConnections()
			query := url.Values{"limit": {strconv.Itoa(limit)}}
			if after != "" {
				query.Set("after", after)
			}
			var result json.RawMessage
			if err := client.get(cmd.Context(), cfg.adminPath(nodeTasks)+"/"+args[0]+"/log", query, &result); err != nil {
				return err
			}
			var page struct {
				Items  []json.RawMessage `json:"items"`
				Cursor string            `json:"cursor"`
			}
			if err := json.Unmarshal(result, &page); err != nil || page.Items == nil || page.Cursor == "" {
				return errors.New("the server returned an invalid task log page")
			}
			columns := []column{{"AT", "at"}, {"ATTEMPT", "attempt"}, {"SEQUENCE", "sequence"}, {"LEVEL", "level"}, {"MESSAGE", "message"}}
			return render(cmd.OutOrStdout(), opts.output, columns, result, page.Items)
		},
	}
	cmd.Flags().StringVar(&after, "after", "", "Continue after the cursor from the previous page")
	cmd.Flags().IntVar(&limit, "limit", maxPage, "Maximum number of log lines to read")
	return cmd
}

func (opts *options) collectFactsCommand() *cobra.Command {
	var key string
	cmd := &cobra.Command{
		Use:     "collect-facts <name-or-id>",
		Short:   "Request an updated hardware and operating system inventory from a node.",
		Example: "  ownsolid node collect-facts \"$NODE_ID\" --idempotency-key \"$REQUEST_KEY\"",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.submitNodeTask(cmd, args[0], key, "/tasks", map[string]string{"operation": "collect_facts.v1"})
		},
	}
	cmd.Flags().StringVar(&key, "idempotency-key", "", "Request UUID (required, reuse the original key on retry)")
	return cmd
}

func (opts *options) submitNodeTask(cmd *cobra.Command, target, key, suffix string, input map[string]string) error {
	if err := requireID(key, "--idempotency-key"); err != nil {
		return err
	}
	cfg, client, err := opts.operator(cmd, "")
	if err != nil {
		return err
	}
	defer client.http.CloseIdleConnections()
	ctx := cmd.Context()
	id, err := client.nodeTarget(ctx, cfg, target)
	if err != nil {
		return err
	}
	var task json.RawMessage
	req := request{method: http.MethodPost, path: cfg.adminPath(nodes) + "/" + id + suffix, status: http.StatusAccepted, key: key}
	if err := client.mutation(ctx, req, input, &task); err != nil {
		return err
	}
	var result struct {
		ID     string `json:"id"`
		NodeID string `json:"node_id"`
	}
	if err := json.Unmarshal(task, &result); err != nil || !resourceID.MatchString(result.ID) || result.NodeID != id {
		return errors.New("the server returned an invalid task or a task for a different node")
	}
	return opts.renderItem(cmd, nodeTasks.columns, task)
}

func (opts *options) nodeInferenceCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "inference <name-or-id>",
		Short:   "Read the node's latest inference and GPU observation.",
		Example: "  ownsolid node inference \"$NODE_ID\" --output json",
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
			var result json.RawMessage
			if err := client.get(ctx, cfg.adminPath(nodes)+"/"+id+"/inference", nil, &result); err != nil {
				return err
			}
			var observation struct {
				NodeID string `json:"node_id"`
			}
			if err := json.Unmarshal(result, &observation); err != nil || observation.NodeID != id {
				return errors.New("the server returned an inference observation for a different or invalid node")
			}
			return opts.renderItem(cmd, []column{{"NODE ID", "node_id"}, {"RECEIVED", "received_at"}, {"STALE", "stale"}}, result)
		},
	}
}

func (opts *options) probeInferenceCommand() *cobra.Command {
	var reason, key string
	cmd := &cobra.Command{
		Use:     "probe-inference <name-or-id>",
		Short:   "Ask a node to refresh its GPU observations for deployment planning.",
		Example: "  ownsolid node probe-inference \"$NODE_ID\" --idempotency-key \"$REQUEST_KEY\" --reason 'Refresh GPU placement'",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			why, err := requiredText(reason, "--reason", 500)
			if err != nil {
				return err
			}
			return opts.submitNodeTask(cmd, args[0], key, "/inference/observations", map[string]string{"reason": why})
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "Reason for the audit record (required)")
	cmd.Flags().StringVar(&key, "idempotency-key", "", "Request UUID (required, reuse the original key on retry)")
	return cmd
}
