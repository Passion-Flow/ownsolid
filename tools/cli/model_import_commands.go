package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"

	"github.com/spf13/cobra"
)

func (opts *options) modelImportsCommand(workspace *string) *cobra.Command {
	group := &cobra.Command{
		Use:     "import",
		Short:   "Upload and verify a locally prepared approved model bundle.",
		Example: "  ownsolid model import catalog\n  ownsolid model import list",
	}
	group.AddCommand(opts.importCatalogCommand(workspace), opts.importListCommand(workspace), opts.importGetCommand(workspace),
		opts.importUploadCommand(workspace, true), opts.importUploadCommand(workspace, false),
		opts.importActionCommand(workspace, false), opts.importActionCommand(workspace, true))
	return group
}

func (opts *options) importCatalogCommand(workspace *string) *cobra.Command {
	return &cobra.Command{
		Use:     "catalog",
		Short:   "Read the approved model catalog, license and bundle limits.",
		Example: "  ownsolid model import catalog --output json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, client, err := opts.operator(cmd, *workspace)
			if err != nil {
				return err
			}
			defer client.http.CloseIdleConnections()
			catalog, raw, err := client.modelCatalog(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			row, err := json.Marshal(map[string]any{
				"catalog_digest": catalog.Digest, "license": catalog.Catalog.License, "minimum_bundle_bytes": catalog.Minimum,
				"maximum_bundle_bytes": catalog.Maximum, "storage_available": catalog.Available,
			})
			if err != nil {
				return err
			}
			columns := []column{{"CATALOG DIGEST", "catalog_digest"}, {"LICENSE", "license"}, {"MINIMUM BYTES", "minimum_bundle_bytes"},
				{"MAXIMUM BYTES", "maximum_bundle_bytes"}, {"STORE AVAILABLE", "storage_available"}}
			return render(cmd.OutOrStdout(), opts.output, columns, raw, []json.RawMessage{row})
		},
	}
}

func (opts *options) importListCommand(workspace *string) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   "List model imports in the selected workspace.",
		Example: "  ownsolid model import list --output json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, client, err := opts.operator(cmd, *workspace)
			if err != nil {
				return err
			}
			defer client.http.CloseIdleConnections()
			ctx := cmd.Context()
			scope, err := client.workspace(ctx, cfg, *workspace)
			if err != nil {
				return err
			}
			items, err := client.list(ctx, cfg.adminPath(modelImports), url.Values{"workspace_id": {scope}}, false)
			if err != nil {
				return err
			}
			return render(cmd.OutOrStdout(), opts.output, modelImports.columns, items, items)
		},
	}
}

func (opts *options) importGetCommand(workspace *string) *cobra.Command {
	return &cobra.Command{
		Use:     "get <import-id>",
		Short:   "Read upload progress, verification state and the published model ID.",
		Example: "  ownsolid model import get \"$IMPORT_ID\" --output json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireID(args[0], "import ID"); err != nil {
				return err
			}
			cfg, client, err := opts.operator(cmd, *workspace)
			if err != nil {
				return err
			}
			defer client.http.CloseIdleConnections()
			ctx := cmd.Context()
			scope, err := client.workspace(ctx, cfg, *workspace)
			if err != nil {
				return err
			}
			return opts.renderDetail(cmd.OutOrStdout(), ctx, client, modelImports, cfg.adminPath(modelImports), args[0], scope)
		},
	}
}

func (opts *options) importUploadCommand(workspace *string, start bool) *cobra.Command {
	var key, name, reason string
	var accept bool
	cmd := &cobra.Command{
		Use:     "resume <import-id> <bundle>",
		Short:   "Resume the same local bundle and queue it for verification.",
		Example: "  ownsolid model import resume \"$IMPORT_ID\" /path/to/model.tar --reason 'Continue approved import'",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			why, err := requiredText(reason, "--reason", 500)
			if err != nil {
				return err
			}
			var id, label string
			if start {
				if !accept {
					return errors.New("read ownsolid model import catalog and pass --accept-license to acknowledge the model license")
				}
				if err := requireID(key, "--idempotency-key"); err != nil {
					return err
				}
				label, err = requiredText(name, "--name", 100)
				if err != nil {
					return err
				}
			} else {
				id = args[0]
				if err := requireID(id, "import ID"); err != nil {
					return err
				}
			}
			file, size, err := openRegularInput(args[len(args)-1], "model bundle")
			if err != nil {
				return err
			}
			defer file.Close()
			return opts.uploadModelImport(cmd, *workspace, id, key, label, why, file, size)
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "Reason for the audit record (required)")
	if start {
		cmd.Use = "start <bundle>"
		cmd.Short = "Start an import, upload the local bundle and queue verification."
		cmd.Example = "  ownsolid model import start /path/to/model.tar --name qwen3 --idempotency-key \"$REQUEST_KEY\" --reason 'Import approved model' --accept-license"
		cmd.Args = cobra.ExactArgs(1)
		cmd.Flags().StringVar(&name, "name", "", "Name for the published model (required)")
		cmd.Flags().StringVar(&key, "idempotency-key", "", "Request UUID (required, reuse the original key on retry)")
		cmd.Flags().BoolVar(&accept, "accept-license", false, "Acknowledge the model license in the approved catalog")
	}
	return cmd
}

func (opts *options) uploadModelImport(cmd *cobra.Command, workspace, id, key, name, reason string, file *os.File, size int64) error {
	cfg, client, err := opts.operator(cmd, workspace)
	if err != nil {
		return err
	}
	defer client.http.CloseIdleConnections()
	ctx := cmd.Context()
	scope, err := client.workspace(ctx, cfg, workspace)
	if err != nil {
		return err
	}
	catalog, _, err := client.modelCatalog(ctx, cfg)
	if err != nil {
		return err
	}
	if !catalog.Available {
		return errors.New("the model store is unavailable; check the installation's model storage before importing")
	}
	path := cfg.adminPath(modelImports)
	var raw json.RawMessage
	if id == "" {
		if size < catalog.Minimum || size > catalog.Maximum {
			return fmt.Errorf("bundle has %d bytes; the approved catalog requires between %d and %d bytes", size, catalog.Minimum, catalog.Maximum)
		}
		req := request{method: http.MethodPost, path: path, key: key, status: http.StatusCreated}
		input := startModelImport{scope, name, catalog.Digest, size, true, reason}
		if err := client.mutation(ctx, req, input, &raw); err != nil {
			return err
		}
	} else {
		raw, err = client.detail(ctx, path, id, scope)
		if err != nil {
			return err
		}
	}
	record, err := decodeModelImport(raw, id, scope)
	if err != nil {
		return err
	}
	if record.Expected != size || record.Digest != catalog.Digest {
		return errors.New("the bundle size or catalog does not match the existing import; choose the original approved bundle")
	}
	switch record.State {
	case "queued", "verifying", "ready":
		return opts.renderItem(cmd, modelImports.columns, raw)
	case "failed":
		return fmt.Errorf("import %s failed verification; inspect it with ownsolid model import get and use verify or cancel", record.ID)
	case "uploading":
	default:
		return fmt.Errorf("import %s is %q and cannot accept chunks", record.ID, record.State)
	}
	if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "Import %s. Resume with ownsolid model import resume %s <bundle> --reason <reason>.\n", record.ID, record.ID); err != nil {
		return err
	}
	if err := client.transferModel(ctx, path, scope, record, file, catalog.Chunk); err != nil {
		return fmt.Errorf("import %s interrupted; resume with the original bundle and workspace: %w", record.ID, err)
	}
	return opts.finishImport(cmd, client, path, scope, record.ID, "complete", reason, http.StatusAccepted)
}

func (opts *options) importActionCommand(workspace *string, cancel bool) *cobra.Command {
	var reason string
	var confirm bool
	action, status := "complete", http.StatusAccepted
	cmd := &cobra.Command{
		Use:     "verify <import-id>",
		Short:   "Queue verification of a completely uploaded bundle.",
		Example: "  ownsolid model import verify \"$IMPORT_ID\" --reason 'Retry verification'",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cancel && !confirm {
				return errors.New("cancellation discards uploaded chunks; pass --confirm to proceed")
			}
			id := args[0]
			if err := requireID(id, "import ID"); err != nil {
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
			ctx := cmd.Context()
			scope, err := client.workspace(ctx, cfg, *workspace)
			if err != nil {
				return err
			}
			path := cfg.adminPath(modelImports)
			if _, err := client.detail(ctx, path, id, scope); err != nil {
				return err
			}
			return opts.finishImport(cmd, client, path, scope, id, action, why, status)
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "Reason for the audit record (required)")
	if cancel {
		action, status = "cancel", http.StatusOK
		cmd.Use = "cancel <import-id>"
		cmd.Short = "Cancel an import and discard its uploaded chunks."
		cmd.Example = "  ownsolid model import cancel \"$IMPORT_ID\" --reason 'Import withdrawn' --confirm"
		cmd.Flags().BoolVar(&confirm, "confirm", false, "Confirm that uploaded chunks will be discarded")
	}
	return cmd
}

func (opts *options) finishImport(cmd *cobra.Command, client *apiClient, path, scope, id, action, reason string, status int) error {
	var raw json.RawMessage
	req := request{method: http.MethodPost, path: path + "/" + id + "/" + action, status: status}
	if err := client.mutation(cmd.Context(), req, map[string]string{"reason": reason}, &raw); err != nil {
		return err
	}
	if _, err := decodeModelImport(raw, id, scope); err != nil {
		return err
	}
	return opts.renderItem(cmd, modelImports.columns, raw)
}
