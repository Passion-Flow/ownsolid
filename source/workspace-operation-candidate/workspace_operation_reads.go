package cli

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/spf13/cobra"
)

type workspaceOperationRead struct {
	RequestID string          `json:"request_id"`
	State     string          `json:"state"`
	Event     json.RawMessage `json:"event"`
}

type workspaceOperationEventRead struct {
	Action      string `json:"action"`
	WorkspaceID string `json:"workspace_id"`
	TargetType  string `json:"target_type"`
	TargetID    string `json:"target_id"`
}

func (opts *options) workspaceOperationCommand() *cobra.Command {
	group := &cobra.Command{
		Use: "operation", Short: "Inspect the retained outcome of your workspace write.",
		Example: "  ownsolid workspace operation get \"$REQUEST_ID\" --output json",
	}
	group.AddCommand(&cobra.Command{
		Use: "get <request-id>", Short: "Read a committed or unconfirmed outcome without repeating a write.",
		Example: "  ownsolid workspace operation get \"$REQUEST_ID\" --output json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			request := args[0]
			if !validWorkspaceReadID(request) {
				return errors.New("request ID must be a canonical lowercase nonzero UUID")
			}
			_, client, err := opts.operator(cmd, "")
			if err != nil {
				return err
			}
			defer client.http.CloseIdleConnections()
			var raw json.RawMessage
			if err := client.get(cmd.Context(), workspaceReadPath+"/operations/"+request, nil, &raw); err != nil {
				return err
			}
			row, err := validateWorkspaceOperationRead(raw, request)
			if err != nil {
				return err
			}
			columns := []column{{"REQUEST ID", "request_id"}, {"STATE", "state"}, {"ACTION", "action"}, {"WORKSPACE", "workspace_id"}}
			return render(cmd.OutOrStdout(), opts.output, columns, raw, []json.RawMessage{row})
		},
	})
	return group
}

func validateWorkspaceOperationRead(raw json.RawMessage, request string) (json.RawMessage, error) {
	bad := errors.New("the server returned an invalid workspace operation receipt; check CLI/server compatibility and retry the read")
	var receipt workspaceOperationRead
	if json.Unmarshal(raw, &receipt) != nil || receipt.RequestID != request || len(receipt.Event) == 0 {
		return nil, bad
	}
	row := map[string]string{"request_id": receipt.RequestID, "state": receipt.State}
	switch receipt.State {
	case "unconfirmed":
		if !bytes.Equal(bytes.TrimSpace(receipt.Event), []byte("null")) {
			return nil, bad
		}
	case "committed":
		var event workspaceOperationEventRead
		if json.Unmarshal(receipt.Event, &event) != nil || !validWorkspaceReadID(event.WorkspaceID) || !validWorkspaceReadID(event.TargetID) {
			return nil, bad
		}
		target := ""
		switch event.Action {
		case "workspace.created", "workspace.updated", "workspace.archived", "workspace.restored":
			target = "workspace"
		case "workspace.member_added", "workspace.member_updated", "workspace.member_removed":
			target = "workspace_membership"
		case "workspace.note_created", "workspace.note_updated", "workspace.note_deleted":
			target = "workspace_note"
		}
		if target == "" || event.TargetType != target || target == "workspace" && event.TargetID != event.WorkspaceID {
			return nil, bad
		}
		row["action"], row["workspace_id"] = event.Action, event.WorkspaceID
	default:
		return nil, bad
	}
	return json.Marshal(row)
}
