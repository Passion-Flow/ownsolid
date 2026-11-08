package cli

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/spf13/cobra"
)

func (opts *options) provisionManagedGatewayCommand(workspace *string) *cobra.Command {
	var name, key, reason string
	cmd := &cobra.Command{
		Use:     "provision <node-id>",
		Short:   "Queue startup of a model gateway managed by the node agent.",
		Example: "  ownsolid deployment alias provision \"$NODE_ID\" --name rack-seven --idempotency-key \"$REQUEST_KEY\" --reason 'Start the node gateway'",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nodeID := args[0]
			if err := requireID(nodeID, "node ID"); err != nil {
				return err
			}
			if err := requireID(key, "--idempotency-key"); err != nil {
				return err
			}
			gatewayName, err := requiredText(name, "--name", 100)
			if err != nil {
				return err
			}
			why, err := requiredText(reason, "--reason", 500)
			if err != nil {
				return err
			}
			path, scope, client, err := opts.distributionClient(cmd, *workspace, gateways)
			if err != nil {
				return err
			}
			defer client.http.CloseIdleConnections()
			payload := struct {
				NodeID string `json:"node_id"`
				Name   string `json:"name"`
				Reason string `json:"reason"`
			}{NodeID: nodeID, Name: gatewayName, Reason: why}
			var result struct {
				Gateway         json.RawMessage `json:"gateway"`
				Secret          *string         `json:"secret"`
				SecretAvailable *bool           `json:"secret_available"`
			}
			req := request{method: http.MethodPost, path: path, key: key, status: http.StatusCreated, otherStatus: http.StatusOK}
			if err := client.mutation(cmd.Context(), req, payload, &result); err != nil {
				return err
			}
			var gateway struct {
				NodeID   string `json:"node_id"`
				Delivery string `json:"delivery"`
			}
			if result.Secret != nil || result.SecretAvailable == nil || *result.SecretAvailable ||
				json.Unmarshal(result.Gateway, &gateway) != nil || gateway.NodeID != nodeID || gateway.Delivery != "agent" {
				return errors.New("unexpected gateway registration result; inspect deployment alias gateways before retrying")
			}
			if err := validateScope(result.Gateway, "", scope); err != nil {
				return err
			}
			return opts.renderItem(cmd, gateways.columns, result.Gateway)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Gateway display name (required)")
	cmd.Flags().StringVar(&key, "idempotency-key", "", "Request UUID (required, reuse the original key on retry)")
	cmd.Flags().StringVar(&reason, "reason", "", "Reason for the audit record (required)")
	return cmd
}
