package cli

import (
	"net/http"

	"github.com/spf13/cobra"
)

const auditEventsPath = "/api/v1/admin/audit-events"
const auditActorHeader = "X-Ownsolid-Audit-Actor"

var auditColumns = []column{
	{"TIME", "occurred_at"}, {"ACTION", "action"}, {"OUTCOME", "outcome"},
	{"ACTOR EMAIL", "actor_email"}, {"ASSISTANT ID", "actor_id"}, {"SUBJECT EMAIL", "subject_email"}, {"REQUEST ID", "request_id"},
}

func (opts *options) auditCommand() *cobra.Command {
	group := &cobra.Command{
		Use:     "audit",
		Short:   "Inspect installation audit events as a platform Admin.",
		Example: "  ownsolid audit list --category auth --limit 20",
	}
	group.AddCommand(opts.listAuditEvents())
	return group
}

func (opts *options) listAuditEvents() *cobra.Command {
	var paging pageOptions
	var assistantActor bool
	filters := []struct {
		flag, parameter, help string
		value                 *string
	}{
		{flag: "action", parameter: "action", help: "Exact action from the server's audit filter catalogue"},
		{flag: "category", parameter: "category", help: "Action family, such as auth or workspace"},
		{flag: "actor-user-id", parameter: "actor_user_id", help: "Human actor UUID"},
		{flag: "outcome", parameter: "outcome", help: "Event outcome: success, failure, or accepted"},
		{flag: "from", parameter: "from", help: "Start of the event window as an RFC 3339 timestamp"},
		{flag: "to", parameter: "to", help: "End of the event window as an RFC 3339 timestamp"},
		{flag: "request-id", parameter: "request_id", help: "Server correlation request UUID"},
		{flag: "target-id", parameter: "target_id", help: "Affected resource UUID"},
	}
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "Read one page of authorized audit events.",
		Example: "  ownsolid audit list --request-id \"$REQUEST_ID\" --assistant-actor --output json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			query, err := paging.query(cmd)
			if err != nil {
				return err
			}
			for _, filter := range filters {
				if cmd.Flags().Changed(filter.flag) {
					query.Set(filter.parameter, *filter.value)
				}
			}
			_, client, err := opts.operator(cmd, "")
			if err != nil {
				return err
			}
			defer client.http.CloseIdleConnections()
			req := request{method: http.MethodGet, path: auditEventsPath, query: query, status: http.StatusOK}
			if assistantActor {
				req.header = http.Header{auditActorHeader: {"true"}}
			}
			var page boundedPage
			if err := client.request(cmd.Context(), req, &page); err != nil {
				return err
			}
			if err := paging.validate(page, "audit event"); err != nil {
				return err
			}
			for _, event := range page.Items {
				if err := validateScope(event, "", ""); err != nil {
					return err
				}
			}
			return opts.renderPage(cmd, auditColumns, page)
		},
	}
	paging.bind(cmd, "Next cursor from the same audit filters")
	for i := range filters {
		filter := &filters[i]
		filter.value = cmd.Flags().String(filter.flag, "", filter.help)
	}
	cmd.Flags().BoolVar(&assistantActor, "assistant-actor", false, "Include verified product assistant attribution when available")
	return cmd
}
