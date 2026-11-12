package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/spf13/cobra"
)

const historyTimeFormat = "2006-01-02T15:04:05Z"

type fleetHistory struct {
	Tier        string             `json:"tier"`
	StepSeconds int64              `json:"step_seconds"`
	From        time.Time          `json:"from"`
	To          time.Time          `json:"to"`
	Cells       []fleetHistoryCell `json:"cells"`
}

type fleetHistoryCell struct {
	SiteID string              `json:"site_id"`
	CellID string              `json:"cell_id"`
	Points []fleetHistoryPoint `json:"points"`
}

type fleetHistoryPoint struct {
	At        time.Time `json:"at"`
	Observed  uint64    `json:"observed"`
	Reachable uint64    `json:"reachable"`
}

func (opts *options) monitoringCommand() *cobra.Command {
	group := &cobra.Command{
		Use: "monitoring", Short: "Read authorized installation monitoring.",
		Example: "  ownsolid monitoring history --from 2026-11-01T00:00:00Z --to 2026-11-01T01:00:00Z",
	}
	var workspace, from, to string
	command := &cobra.Command{
		Use: "history", Short: "Read sparse fleet history for a workspace.",
		Example: "  ownsolid monitoring history --workspace \"$WORKSPACE_ID\" --from 2026-11-01T00:00:00Z --to 2026-11-01T01:00:00Z --output json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			start, err := parseHistoryTime(from, "--from")
			if err != nil {
				return err
			}
			end, err := parseHistoryTime(to, "--to")
			if err != nil {
				return err
			}
			if !start.Before(end) {
				return errors.New("--from must be earlier than --to")
			}
			cfg, client, err := opts.operator(cmd, workspace)
			if err != nil {
				return err
			}
			defer client.http.CloseIdleConnections()
			scope, err := client.workspace(cmd.Context(), cfg, workspace)
			if err != nil {
				return err
			}
			query := url.Values{"workspace_id": {scope}, "from": {from}, "to": {to}}
			var result fleetHistory
			if err := client.get(cmd.Context(), cfg.adminPath(resource{key: "monitoring"})+"/fleet-history", query, &result); err != nil {
				return err
			}
			if result.Tier != "raw" && result.Tier != "rollup" || result.StepSeconds <= 0 ||
				result.From.Before(start) || result.To.After(end) || result.From.After(result.To) || result.Cells == nil {
				return errors.New("the server returned invalid fleet history; check CLI/server compatibility and retry")
			}
			var rows []json.RawMessage
			for _, cell := range result.Cells {
				for _, point := range cell.Points {
					row, err := json.Marshal(struct {
						SiteID string `json:"site_id"`
						CellID string `json:"cell_id"`
						fleetHistoryPoint
					}{cell.SiteID, cell.CellID, point})
					if err != nil {
						return err
					}
					rows = append(rows, row)
				}
			}
			if opts.output == "table" {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "TIER %s  STEP %ds  FROM %s  TO %s\n",
					result.Tier, result.StepSeconds, result.From.Format(historyTimeFormat), result.To.Format(historyTimeFormat)); err != nil {
					return err
				}
			}
			return render(cmd.OutOrStdout(), opts.output, []column{
				{"SITE", "site_id"}, {"CELL", "cell_id"}, {"AT", "at"},
				{"OBSERVED", "observed"}, {"REACHABLE", "reachable"},
			}, result, rows)
		},
	}
	command.Flags().StringVar(&workspace, "workspace", "", "Workspace UUID (defaults to the configured or current workspace)")
	command.Flags().StringVar(&from, "from", "", "Start time as UTC seconds, for example 2026-11-01T00:00:00Z")
	command.Flags().StringVar(&to, "to", "", "End time as UTC seconds, for example 2026-11-01T01:00:00Z")
	group.AddCommand(command)
	return group
}

func parseHistoryTime(raw, name string) (time.Time, error) {
	at, err := time.Parse(historyTimeFormat, raw)
	if err != nil || at.Unix() < 0 || at.Format(historyTimeFormat) != raw {
		return time.Time{}, fmt.Errorf("%s must be UTC seconds in YYYY-MM-DDTHH:MM:SSZ format", name)
	}
	return at, nil
}
