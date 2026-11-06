package nodes

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
)

const (
	// maximumLogPage bounds the lines of one log read.
	maximumLogPage = 500
	// taskSummaryColumns are the columns node_tasks and node_task_history share that a list shows.
	taskSummaryColumns = `id,tenant_id,workspace_id,node_id,operation,state,attempt,error_code,created_at,updated_at,completed_at`
	// taskSummaryFields reads a page of tasks with the name of each node, which outlives a
	// removed node's listing and is missing only if the node's row has gone.
	taskSummaryFields = `t.id::text,t.workspace_id::text,t.node_id::text,COALESCE(n.name,t.node_id::text),t.operation,t.state,t.attempt,t.error_code,t.created_at,t.updated_at,t.completed_at`
)

var logCursorPattern = regexp.MustCompile(`^([0-9]{1,2})\.([0-9]{1,5})$`)

// TaskSummary is a task as an administrator's list shows it.
type TaskSummary struct {
	ID          string     `json:"id"`
	WorkspaceID string     `json:"workspace_id"`
	NodeID      string     `json:"node_id"`
	NodeName    string     `json:"node_name"`
	Operation   string     `json:"operation"`
	State       string     `json:"state"`
	Attempt     int        `json:"attempt"`
	ErrorCode   *string    `json:"error_code"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	CompletedAt *time.Time `json:"completed_at"`
}

// TaskStep is one step of a task's latest attempt.
type TaskStep struct {
	Step       string     `json:"step"`
	State      string     `json:"state"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

// TaskDetail is a task and the steps of its latest attempt.
type TaskDetail struct {
	Task  TaskSummary `json:"task"`
	Steps []TaskStep  `json:"steps"`
}

// TaskLogLine is one line of a task's log.
type TaskLogLine struct {
	Attempt  int       `json:"attempt"`
	Sequence int       `json:"sequence"`
	At       time.Time `json:"at"`
	Level    string    `json:"level"`
	Message  string    `json:"message"`
}

// TaskLog is lines of a task's log and the cursor to read on from.
type TaskLog struct {
	Items  []TaskLogLine `json:"items"`
	Cursor string        `json:"cursor"`
}

// stepReport is one step change, as a task reported it.
type stepReport struct {
	step  string
	state string
	at    time.Time
}

func finishedTaskState(state string) bool {
	return state == "succeeded" || state == "failed" || state == "cancelled"
}

// foldSteps turns step changes into steps. The agent closes each step it opens, but one that
// died cannot: a step still open on a finished task takes the task's end and outcome, so a
// timeline never shows work running that has stopped.
func foldSteps(reports []stepReport, task TaskSummary) []TaskStep {
	out := []TaskStep{}
	open := map[string]int{}
	for _, report := range reports {
		if report.state == wire.ProgressStepRunning {
			open[report.step] = len(out)
			out = append(out, TaskStep{Step: report.step, State: wire.ProgressStepRunning, StartedAt: report.at})
			continue
		}
		if index, found := open[report.step]; found {
			finished := report.at
			out[index].State, out[index].FinishedAt = report.state, &finished
			delete(open, report.step)
		}
	}
	if !finishedTaskState(task.State) {
		return out
	}
	outcome := wire.ProgressStepFailed
	if task.State == "succeeded" {
		outcome = wire.ProgressStepSucceeded
	}
	for _, index := range open {
		out[index].State, out[index].FinishedAt = outcome, task.CompletedAt
	}
	return out
}

// taskReader runs a read of the task views, which only PostgreSQL has (ADR 0029).
func (s *Service) taskReader(ctx context.Context, a identity.Auth, fn func(context.Context, transaction.Executor, string) error) error {
	return s.readActor(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		if !isAdmin(a) {
			return fail("ACCESS_DENIED")
		}
		if q.Dialect() != transaction.PostgreSQL {
			return fail("TASK_HISTORY_UNAVAILABLE")
		}
		tenant, e := nodeTenant(ctx, q)
		if e != nil {
			return e
		}
		return fn(ctx, q, tenant)
	})
}

func scanTaskSummary(row transaction.Row) (TaskSummary, error) {
	var v TaskSummary
	e := row.Scan(&v.ID, &v.WorkspaceID, &v.NodeID, &v.NodeName, &v.Operation, &v.State, &v.Attempt, &v.ErrorCode, &v.CreatedAt, &v.UpdatedAt, &v.CompletedAt)
	if errors.Is(e, sql.ErrNoRows) {
		return v, fail("RESOURCE_NOT_FOUND")
	}
	if e != nil {
		return TaskSummary{}, e
	}
	v.CreatedAt, v.UpdatedAt = v.CreatedAt.UTC(), v.UpdatedAt.UTC()
	if v.CompletedAt != nil {
		stamp := v.CompletedAt.UTC()
		v.CompletedAt = &stamp
	}
	return v, nil
}

// taskListFilter is what a task list asks for beyond its page.
type taskListFilter struct {
	state string
	mine  bool
}

// taskListQuery splits the filters from the paging values and checks them.
func taskListQuery(values url.Values) (taskListFilter, url.Values, error) {
	var filter taskListFilter
	page := url.Values{}
	for key, value := range values {
		if len(value) != 1 {
			return filter, nil, fail("VALIDATION_FAILED")
		}
		switch key {
		case "state":
			filter.state = value[0]
			if !finishedTaskState(filter.state) && filter.state != "pending" && filter.state != "running" {
				return filter, nil, fail("VALIDATION_FAILED")
			}
		case "mine":
			if value[0] != "true" {
				return filter, nil, fail("VALIDATION_FAILED")
			}
			filter.mine = true
		default:
			page[key] = value
		}
	}
	return filter, page, nil
}

// taskListStatement reads one page. Unfinished tasks are in node_tasks and finished ones in the
// history, so a filter reads one side, and each side keeps its own index order. The page is cut
// before node names are joined.
func taskListStatement(tenant, user string, filter taskListFilter, cursor pagination.Cursor, limit int) transaction.Statement {
	args := []any{tenant}
	seek := ""
	if cursor.ID != "" {
		args = append(args, cursor.Time, cursor.ID)
		seek = " AND (created_at,id)<($2,$3::uuid)"
	}
	next := func(value any) string {
		args = append(args, value)
		return "$" + strconv.Itoa(len(args))
	}
	live := "SELECT " + taskSummaryColumns + " FROM node_tasks WHERE tenant_id=$1 AND completed_at IS NULL" + seek
	history := "SELECT " + taskSummaryColumns + " FROM node_task_history WHERE tenant_id=$1" + seek
	if filter.mine {
		live += " AND requested_by=" + next(user) + "::uuid"
	}
	source := live + " UNION ALL " + history
	switch {
	case filter.state != "" && finishedTaskState(filter.state):
		source = history + " AND state=" + next(filter.state)
	case filter.state != "":
		source = live + " AND state=" + next(filter.state)
	case filter.mine:
		source = live
	}
	return transaction.SQL(transaction.PostgreSQL, "SELECT "+taskSummaryFields+" FROM ("+source+" ORDER BY created_at DESC,id DESC LIMIT "+next(limit+1)+") t LEFT JOIN managed_nodes n ON n.id=t.node_id ORDER BY t.created_at DESC,t.id DESC", args...)
}

// AdminTasks lists tasks across every node of the tenant, newest first.
func (s *Service) AdminTasks(ctx context.Context, a identity.Auth, values url.Values) (Page[TaskSummary], error) {
	var out Page[TaskSummary]
	filter, page, e := taskListQuery(values)
	if e != nil {
		return out, e
	}
	e = s.taskReader(ctx, a, func(ctx context.Context, q transaction.Executor, tenant string) error {
		scope := nodeListScope("admin-tasks:"+filter.state+":"+strconv.FormatBool(filter.mine), tenant, "", "", a)
		limit, cursor, fingerprint, e := pagination.ParseWithDefault(page, scope, s.Identity.Config.Policy().UI.PageSize)
		if e != nil {
			return e
		}
		out, e = nodeListRows(ctx, q, taskListStatement(tenant, a.Me.User.ID, filter, cursor, limit), limit, fingerprint, scanTaskSummary,
			func(v TaskSummary) (time.Time, string) { return v.CreatedAt, v.ID })
		return e
	})
	if e != nil {
		return Page[TaskSummary]{}, e
	}
	return out, nil
}

// adminTaskWithin reads one task of the tenant, from either side of the history.
func adminTaskWithin(ctx context.Context, q transaction.Executor, tenant, id string) (TaskSummary, error) {
	return scanTaskSummary(q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT "+taskSummaryFields+" FROM (SELECT "+taskSummaryColumns+" FROM node_tasks WHERE tenant_id=$1 AND id=$2::uuid UNION ALL SELECT "+taskSummaryColumns+" FROM node_task_history WHERE tenant_id=$1 AND id=$2::uuid LIMIT 1) t LEFT JOIN managed_nodes n ON n.id=t.node_id", tenant, id)))
}

// AdminTask reads a task and the steps of its latest attempt.
func (s *Service) AdminTask(ctx context.Context, a identity.Auth, id string) (TaskDetail, error) {
	var out TaskDetail
	if !nodeID(id) {
		return out, fail("VALIDATION_FAILED")
	}
	e := s.taskReader(ctx, a, func(ctx context.Context, q transaction.Executor, tenant string) error {
		task, e := adminTaskWithin(ctx, q, tenant, id)
		if e != nil {
			return e
		}
		rows, e := q.Query(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT step,step_state,reported_at FROM node_task_progress WHERE task_id=$1::uuid AND task_created_at=$2 AND attempt=$3 AND kind='step' ORDER BY sequence`, id, task.CreatedAt, task.Attempt))
		if e != nil {
			return e
		}
		defer rows.Close()
		reports := []stepReport{}
		for rows.Next() {
			var report stepReport
			if e = rows.Scan(&report.step, &report.state, &report.at); e != nil {
				return e
			}
			report.at = report.at.UTC()
			reports = append(reports, report)
		}
		if e = rows.Err(); e != nil {
			return e
		}
		out = TaskDetail{Task: task, Steps: foldSteps(reports, task)}
		return nil
	})
	if e != nil {
		return TaskDetail{}, e
	}
	return out, nil
}

// logQuery reads where a log read starts and how many lines it asks for.
func logQuery(values url.Values) (attempt, sequence, limit int, e error) {
	limit = maximumLogPage
	for key, value := range values {
		if len(value) != 1 {
			return 0, 0, 0, fail("VALIDATION_FAILED")
		}
		switch key {
		case "after":
			match := logCursorPattern.FindStringSubmatch(value[0])
			if match == nil {
				return 0, 0, 0, fail("INVALID_CURSOR")
			}
			attempt, _ = strconv.Atoi(match[1])
			sequence, _ = strconv.Atoi(match[2])
		case "limit":
			var bad error
			limit, bad = strconv.Atoi(value[0])
			if bad != nil || limit < 1 || limit > maximumLogPage {
				return 0, 0, 0, fail("VALIDATION_FAILED")
			}
		default:
			return 0, 0, 0, fail("VALIDATION_FAILED")
		}
	}
	return attempt, sequence, limit, nil
}

// AdminTaskLog reads a task's log from just after the cursor. A read past the end returns no
// lines and the same cursor, so a page that polls asks only for what is new.
func (s *Service) AdminTaskLog(ctx context.Context, a identity.Auth, id string, values url.Values) (TaskLog, error) {
	var out TaskLog
	if !nodeID(id) {
		return out, fail("VALIDATION_FAILED")
	}
	attempt, sequence, limit, e := logQuery(values)
	if e != nil {
		return out, e
	}
	e = s.taskReader(ctx, a, func(ctx context.Context, q transaction.Executor, tenant string) error {
		task, e := adminTaskWithin(ctx, q, tenant, id)
		if e != nil {
			return e
		}
		rows, e := q.Query(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT attempt,sequence,reported_at,level,message FROM node_task_progress WHERE task_id=$1::uuid AND task_created_at=$2 AND (attempt,sequence)>($3,$4) AND kind='log' ORDER BY attempt,sequence LIMIT $5`, id, task.CreatedAt, attempt, sequence, limit))
		if e != nil {
			return e
		}
		defer rows.Close()
		out = TaskLog{Items: []TaskLogLine{}, Cursor: fmt.Sprintf("%d.%d", attempt, sequence)}
		for rows.Next() {
			var line TaskLogLine
			if e = rows.Scan(&line.Attempt, &line.Sequence, &line.At, &line.Level, &line.Message); e != nil {
				return e
			}
			line.At = line.At.UTC()
			out.Items = append(out.Items, line)
			out.Cursor = fmt.Sprintf("%d.%d", line.Attempt, line.Sequence)
		}
		return rows.Err()
	})
	if e != nil {
		return TaskLog{}, e
	}
	return out, nil
}
