package runtime

import (
	"context"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/client"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/progress"
)

// taskReporter reports a task's progress through the task path taskBase, under its lease.
func taskReporter(connection *client.Client, taskBase string, lease wire.Lease) *progress.Reporter {
	send := func(ctx context.Context, entries []wire.ProgressEntry) error {
		var accepted wire.TaskProgressAccepted
		report := wire.TaskProgress{LeaseToken: lease.LeaseToken, Entries: entries}
		return connection.Post(ctx, taskBase+"/"+wire.TaskProgressPath, report, &accepted)
	}
	return progress.New(lease.TaskID, send, progressRetryable)
}

// progressRetryable keeps entries for the next report while control sheds telemetry or
// cannot be reached (ADR 0018). A refusal means control does not want this report, which
// is what an older control or a lost lease answers, so reporting ends and the task goes on.
func progressRetryable(e error) bool {
	if _, shed := client.RetryHint(e); shed {
		return true
	}
	return e.Error() == "CONTROL_CONNECTION_FAILED"
}
