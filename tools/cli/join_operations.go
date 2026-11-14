package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var joinTokens = resource{key: "node-join-tokens", columns: []column{
	{"ID", "id"}, {"NODE", "node_name"}, {"STATUS", "status"}, {"PROFILE", "execution_profile"}, {"EXPIRES", "expires_at"},
}}

func (opts *options) joinTokensCommand() *cobra.Command {
	group := &cobra.Command{
		Use:     "join-token",
		Short:   "Manage single-use invitations for node enrollment.",
		Example: "  ownsolid node join-token list",
	}
	var all bool
	list := &cobra.Command{
		Use:     "list",
		Short:   "List node enrollment invitations without their credentials.",
		Example: "  ownsolid node join-token list --output json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, client, err := opts.operator(cmd, "")
			if err != nil {
				return err
			}
			defer client.http.CloseIdleConnections()
			query := make(url.Values)
			if !all {
				query.Set("status", "pending")
			}
			items, err := client.list(cmd.Context(), cfg.adminPath(joinTokens), query, true)
			if err != nil {
				return err
			}
			return render(cmd.OutOrStdout(), opts.output, joinTokens.columns, items, items)
		},
	}
	list.Flags().BoolVar(&all, "all", false, "Include used, revoked, and expired invitations")
	group.AddCommand(list, opts.createJoinCommand(), opts.revokeJoinCommand())
	return group
}

func (opts *options) createJoinCommand() *cobra.Command {
	var workspace, profile, target string
	cmd := &cobra.Command{
		Use:     "create <node-name>",
		Short:   "Issue a single-use invitation and save its credential in a private file.",
		Example: "  ownsolid node join-token create gpu-worker-01 --token-file \"$PRIVATE_DIR/enrollment-token\"",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := requiredText(args[0], "node name", 100)
			if err != nil {
				return err
			}
			if !filepath.IsAbs(target) || filepath.Clean(target) == "/" {
				return errors.New("--token-file must be an absolute path to a new file in a directory you own with mode 0700")
			}
			return opts.createJoinToken(cmd, workspace, target, name, profile)
		},
	}
	cmd.Flags().StringVar(&workspace, "workspace", "", "Workspace UUID (defaults to the session's current workspace)")
	cmd.Flags().StringVar(&profile, "profile", "read_only", "Agent execution profile")
	cmd.Flags().StringVar(&target, "token-file", "", "Absolute path to a new mode 0600 credential file (required)")
	return cmd
}

func (opts *options) createJoinToken(cmd *cobra.Command, workspace, target, name, profile string) error {
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
	root, err := os.OpenRoot(filepath.Dir(target))
	if err != nil {
		return fmt.Errorf("open the credential directory: %w", err)
	}
	defer root.Close()
	info, err := root.Stat(".")
	if err != nil {
		return fmt.Errorf("inspect the credential directory: %w", err)
	}
	if !ownedMode(info, 0o700) {
		return errors.New("the credential directory must be owned by you and have mode 0700")
	}
	filename := filepath.Base(target)
	file, err := root.OpenFile(filename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("reserve a new credential file; choose an unused --token-file: %w", err)
	}
	saved := false
	defer func() {
		if !saved {
			file.Close()
			root.Remove(filename)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return err
	}

	input := map[string]string{"workspace_id": workspace, "node_name": name, "execution_profile": profile}
	req := request{method: http.MethodPost, path: cfg.adminPath(joinTokens), status: http.StatusCreated}
	var result json.RawMessage
	if err := client.mutation(ctx, req, input, &result); err != nil {
		return fmt.Errorf("if issuance was interrupted, inspect ownsolid node join-token list before creating another invitation: %w", err)
	}
	if err := validateScope(result, "", workspace); err != nil {
		return fmt.Errorf("inspect pending invitations before retrying: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(result, &fields); err != nil {
		return err
	}
	var secret string
	if err := json.Unmarshal(fields["token"], &secret); err != nil || !validSession(secret) {
		return errors.New("the server returned an invalid enrollment credential; inspect pending invitations before retrying")
	}
	delete(fields, "token")
	if _, err := io.WriteString(file, secret+"\n"); err != nil {
		return fmt.Errorf("save the enrollment credential; revoke the pending invitation before retrying: %w", err)
	}
	if err := errors.Join(file.Sync(), file.Close()); err != nil {
		return fmt.Errorf("persist the enrollment credential; revoke the pending invitation before retrying: %w", err)
	}
	saved = true
	metadata, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	return opts.renderItem(cmd, joinTokens.columns, metadata)
}

func (opts *options) revokeJoinCommand() *cobra.Command {
	var reason string
	cmd := &cobra.Command{
		Use:     "revoke <token-id>",
		Short:   "Revoke an unused node enrollment invitation.",
		Example: "  ownsolid node join-token revoke \"$TOKEN_ID\" --reason 'Node installation postponed'",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireID(args[0], "token ID"); err != nil {
				return err
			}
			why, err := requiredText(reason, "--reason", 500)
			if err != nil {
				return err
			}
			cfg, client, err := opts.operator(cmd, "")
			if err != nil {
				return err
			}
			defer client.http.CloseIdleConnections()
			var result json.RawMessage
			req := request{method: http.MethodPost, path: cfg.adminPath(joinTokens) + "/" + args[0] + "/revocations", status: http.StatusOK}
			if err := client.mutation(cmd.Context(), req, map[string]string{"reason": why}, &result); err != nil {
				return err
			}
			if err := validateScope(result, args[0], ""); err != nil {
				return err
			}
			return opts.renderItem(cmd, joinTokens.columns, result)
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "Reason for the audit record (required)")
	return cmd
}
