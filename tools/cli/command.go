package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"runtime/debug"

	"github.com/spf13/cobra"
)

type options struct {
	server string
	output string
}

// NewCommand builds the operator CLI and its subcommands.
func NewCommand() *cobra.Command {
	opts := options{}
	root := &cobra.Command{
		Use:           "ownsolid",
		Short:         "Operate the ownsolid Admin control plane from a terminal.",
		Example:       "  ownsolid --server \"$ADMIN_ORIGIN\" login\n  ownsolid node list --output json",
		SilenceUsage:  true,
		SilenceErrors: true,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			switch opts.output {
			case "table", "json", "yaml":
				return nil
			default:
				return errors.New("--output must be table, json, or yaml")
			}
		},
	}
	root.SetHelpCommand(&cobra.Command{
		Use:     "help [command]",
		Short:   "Show help for a command.",
		Example: "  ownsolid help node list",
		RunE: func(cmd *cobra.Command, args []string) error {
			target, remaining, err := root.Find(args)
			if err != nil || len(remaining) > 0 {
				return errors.New("unknown help topic; run ownsolid --help to see the commands")
			}
			return target.Help()
		},
	})
	root.PersistentFlags().StringVar(&opts.server, "server", "", "Admin origin (overrides OWNSOLID_SERVER and config.json)")
	root.PersistentFlags().StringVarP(&opts.output, "output", "o", "table", "Output format: table, json, or yaml")
	root.AddCommand(
		opts.loginCommand(), opts.logoutCommand(), versionCommand(),
		opts.resourceCommand(nodes), opts.resourceCommand(models), opts.resourceCommand(deployments),
		opts.trainingCommand(), opts.gatewayCommand(), opts.auditCommand(), opts.monitoringCommand(), opts.workspaceCommand(), opts.browserSessionsCommand(), opts.userCommand(),
	)
	return root
}

func (opts *options) settings(cmd *cobra.Command) (settings, error) {
	return loadSettings(opts.server, cmd.Flags().Changed("server"))
}

func (opts *options) resourceCommand(r resource) *cobra.Command {
	group := &cobra.Command{
		Use:     r.singular,
		Short:   "Manage " + r.key + " from the Admin control plane.",
		Example: "  ownsolid " + r.singular + " list",
	}
	var workspace string
	if r.key != nodes.key {
		group.PersistentFlags().StringVar(&workspace, "workspace", "", "Workspace UUID (defaults to the session's current workspace)")
	}
	group.AddCommand(&cobra.Command{
		Use:     "list",
		Short:   "List " + r.key + " visible in this scope.",
		Example: "  ownsolid " + r.singular + " list\n  ownsolid " + r.singular + " list --output json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return opts.readResource(cmd, r, workspace, "")
		},
	})
	switch r.key {
	case nodes.key:
		group.AddCommand(opts.getCommand(r, &workspace, "Show a node by exact name or UUID.", "gpu-worker-01"))
		opts.addNodeOperations(group)
	case deployments.key:
		group.AddCommand(opts.getCommand(r, &workspace, "Show a deployment by UUID or by its model name when unique.", "qwen3"))
		opts.addDeploymentOperations(group, &workspace)
		group.AddCommand(opts.aliasCommand(&workspace))
	case models.key:
		group.AddCommand(opts.modelGetCommand(&workspace), opts.retireModelCommand(&workspace), opts.modelImportsCommand(&workspace))
	}
	return group
}

func (opts *options) getCommand(r resource, workspace *string, description, example string) *cobra.Command {
	return &cobra.Command{
		Use:     "get <name-or-id>",
		Short:   description,
		Example: "  ownsolid " + r.singular + " get " + example + " --output yaml",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] == "" {
				return errors.New("supply a resource name or UUID")
			}
			return opts.readResource(cmd, r, *workspace, args[0])
		},
	}
}

func (opts *options) readResource(cmd *cobra.Command, r resource, workspace, name string) error {
	cfg, client, err := opts.operator(cmd, workspace)
	if err != nil {
		return err
	}
	defer client.http.CloseIdleConnections()
	path, ctx := cfg.adminPath(r), cmd.Context()
	id := lookupID(name)
	if id != "" {
		if r.key == deployments.key && workspace == "" {
			workspace = cfg.WorkspaceID
		}
		return opts.renderDetail(cmd.OutOrStdout(), ctx, client, r, path, id, workspace)
	}

	query := make(url.Values)
	if r.key != nodes.key {
		workspace, err = client.workspace(ctx, cfg, workspace)
		if err != nil {
			return err
		}
		query.Set("workspace_id", workspace)
	}
	if name == "" {
		items, err := client.list(ctx, path, query, r.key == nodes.key)
		if err != nil {
			return err
		}
		return render(cmd.OutOrStdout(), opts.output, r.columns, items, items)
	}
	if r.key == nodes.key {
		id, err = client.resolveNode(ctx, cfg, name)
	} else {
		id, err = client.resolveDeployment(ctx, cfg, workspace, name)
	}
	if err != nil {
		return err
	}
	return opts.renderDetail(cmd.OutOrStdout(), ctx, client, r, path, id, workspace)
}

func (opts *options) operator(cmd *cobra.Command, workspace string) (settings, *apiClient, error) {
	if cmd.Flags().Changed("workspace") && !resourceID.MatchString(workspace) {
		return settings{}, nil, errors.New("--workspace must be a UUID")
	}
	cfg, err := opts.settings(cmd)
	if err != nil {
		return settings{}, nil, err
	}
	store, err := openSessionStore(cfg, false)
	if errors.Is(err, os.ErrNotExist) {
		return settings{}, nil, noSession(cfg)
	}
	if err != nil {
		return settings{}, nil, err
	}
	value, err := store.read()
	store.root.Close()
	if err != nil {
		return settings{}, nil, err
	}
	if value == "" {
		return settings{}, nil, noSession(cfg)
	}
	client, err := newClient(cfg)
	if err != nil {
		return settings{}, nil, err
	}
	client.restoreSession(value)
	return cfg, client, nil
}

func (opts *options) renderDetail(w io.Writer, ctx context.Context, client *apiClient, r resource, path, id, workspace string) error {
	item, err := client.detail(ctx, path, id, workspace)
	if err != nil {
		return err
	}
	return render(w, opts.output, r.columns, item, []json.RawMessage{item})
}

func (c *apiClient) detail(ctx context.Context, path, id, workspace string) (json.RawMessage, error) {
	var item json.RawMessage
	if err := c.get(ctx, path+"/"+id, nil, &item); err != nil {
		return nil, err
	}
	return item, validateScope(item, id, workspace)
}

func validateScope(item json.RawMessage, id, workspace string) error {
	var actual struct {
		ID          string `json:"id"`
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.Unmarshal(item, &actual); err != nil || !resourceID.MatchString(actual.ID) || id != "" && actual.ID != id {
		return errors.New("the server returned a different or invalid resource ID")
	}
	if workspace != "" && actual.WorkspaceID != workspace {
		return fmt.Errorf("the resource is outside the selected workspace %s; check --workspace or workspace_id in config.json", workspace)
	}
	return nil
}

func noSession(cfg settings) error {
	return fmt.Errorf("no saved Admin session for %s; run ownsolid login using the same server", cfg.origin.String())
}

func versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Short:   "Print the installed CLI version without contacting the server.",
		Example: "  ownsolid version\n  ownsolid version --output json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			version := "dev"
			result := make(map[string]string)
			if info, ok := debug.ReadBuildInfo(); ok {
				if info.Main.Version != "" && info.Main.Version != "(devel)" {
					version = info.Main.Version
				}
				for _, setting := range info.Settings {
					if setting.Key == "vcs.revision" {
						result["revision"] = setting.Value
					}
				}
			}
			result["version"] = version
			return renderValue(cmd, []column{{"VERSION", "version"}, {"REVISION", "revision"}}, result)
		},
	}
}
