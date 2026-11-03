package workspaces

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const noteFields = `id::text,tenant_id::text,workspace_id::text,title,text,author_user_id::text,version,created_at,updated_at`
const MaximumNoteTextBytes = 8192

func scanNote(row transaction.Row) (Note, error) {
	var n Note
	e := row.Scan(&n.ID, &n.TenantID, &n.WorkspaceID, &n.Title, &n.Text, &n.AuthorUserID, &n.Version, &n.CreatedAt, &n.UpdatedAt)
	if errors.Is(e, sql.ErrNoRows) {
		return Note{}, fail("RESOURCE_NOT_FOUND")
	}
	return n, e
}
func noteInput(title, text string) (string, error) {
	title, e := security.Name(title)
	if e != nil {
		return "", e
	}
	if !utf8.ValidString(text) || strings.ContainsRune(text, '\x00') || len(text) > MaximumNoteTextBytes {
		return "", fail("VALIDATION_FAILED")
	}
	return title, nil
}
func selectNoteWithin(ctx context.Context, q transaction.Executor, w Workspace, id string, lock bool) (Note, error) {
	pg := "SELECT " + noteFields + " FROM workspace_notes WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 AND deleted_at IS NULL"
	my := "SELECT " + mysqlNoteFields + " FROM workspace_notes WHERE tenant_id=? AND workspace_id=? AND id=? AND deleted_at IS NULL"
	dm := "SELECT " + damengNoteFields + " FROM WORKSPACE_NOTES WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ID=? AND DELETED_AT IS NULL"
	if lock {
		pg += " FOR UPDATE"
		my += " FOR UPDATE"
		dm += " FOR UPDATE"
	}
	return scanNote(q.QueryRow(ctx, workspaceSQL(q, pg, my, dm, w.TenantID, w.ID, id)))
}
func (s *Service) CreateNote(ctx context.Context, a identity.Auth, workspace, title, text, requestID string) (Note, error) {
	if !workspaceID(workspace) {
		return Note{}, fail("VALIDATION_FAILED")
	}
	title, e := noteInput(title, text)
	if e != nil {
		return Note{}, e
	}
	var out Note
	e = s.mutation(ctx, a, nil, false, requestID, func(ctx context.Context, q transaction.Executor, _ string) error {
		w, e := s.writableWithin(ctx, q, a, workspace, false)
		if e != nil {
			return e
		}
		var count int
		if e = q.QueryRow(ctx, workspaceSQL(q, "SELECT count(*) FROM workspace_notes WHERE tenant_id=$1 AND workspace_id=$2 AND deleted_at IS NULL", "SELECT count(*) FROM workspace_notes WHERE tenant_id=? AND workspace_id=? AND deleted_at IS NULL", `SELECT COUNT(*) FROM WORKSPACE_NOTES WHERE TENANT_ID=? AND WORKSPACE_ID=? AND DELETED_AT IS NULL`, w.TenantID, w.ID)).Scan(&count); e != nil {
			return e
		}
		if count >= s.Identity.Config.Policy().Workspaces.MaximumLiveNotes {
			return fail("RESOURCE_LIMIT_REACHED")
		}
		id := uuid.NewString()
		insert := workspaceSQL(q, "INSERT INTO workspace_notes(id,tenant_id,workspace_id,title,text,author_user_id) VALUES($1,$2,$3,$4,$5,$6)", "INSERT INTO workspace_notes(id,tenant_id,workspace_id,title,text,author_user_id) VALUES(?,?,?,?,?,?)", `INSERT INTO WORKSPACE_NOTES(ID,TENANT_ID,WORKSPACE_ID,TITLE,TEXT,AUTHOR_USER_ID) VALUES(?,?,?,?,?,?)`, id, w.TenantID, w.ID, title, text, a.Me.User.ID)
		if q.Dialect() == transaction.Dameng && text == "" {
			insert = transaction.SQL(transaction.Dameng, `INSERT INTO WORKSPACE_NOTES(ID,TENANT_ID,WORKSPACE_ID,TITLE,TEXT,AUTHOR_USER_ID) VALUES(?,?,?,?,EMPTY_CLOB(),?)`, id, w.TenantID, w.ID, title, a.Me.User.ID)
		}
		n, e := q.Exec(ctx, insert)
		if e != nil {
			return e
		}
		if n != 1 {
			return fail("WORKSPACE_WRITE_UNCONFIRMED")
		}
		out, e = selectNoteWithin(ctx, q, w, id, false)
		if e != nil {
			return e
		}
		return s.scopedAuditWithin(ctx, q, a, w, "workspace.note_created", "workspace_note", id, "", "", requestID)
	})
	if e != nil {
		return Note{}, e
	}
	return out, nil
}
func (s *Service) ChangeNote(ctx context.Context, a identity.Auth, workspace, id, title, text string, version int64, requestID string, remove bool) (Note, error) {
	if !workspaceID(workspace) || !workspaceID(id) || !ValidVersion(version) {
		return Note{}, fail("VALIDATION_FAILED")
	}
	var e error
	if !remove {
		title, e = noteInput(title, text)
		if e != nil {
			return Note{}, e
		}
	}
	var out Note
	e = s.mutation(ctx, a, nil, false, requestID, func(ctx context.Context, q transaction.Executor, _ string) error {
		w, e := s.writableWithin(ctx, q, a, workspace, false)
		if e != nil {
			return e
		}
		n, e := selectNoteWithin(ctx, q, w, id, true)
		if e != nil {
			return e
		}
		if n.Version != version {
			return fail("VERSION_CONFLICT")
		}
		if e = incrementable(n.Version); e != nil {
			return e
		}
		action := "workspace.note_updated"
		var affected int64
		if remove {
			action = "workspace.note_deleted"
			affected, e = q.Exec(ctx, workspaceSQL(q, "UPDATE workspace_notes SET deleted_at=clock_timestamp(),version=version+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3", "UPDATE workspace_notes SET deleted_at=UTC_TIMESTAMP(6),version=version+1,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=?", `UPDATE WORKSPACE_NOTES SET DELETED_AT=CURRENT_TIMESTAMP(6),VERSION=VERSION+1,UPDATED_AT=CURRENT_TIMESTAMP(6) WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ID=?`, w.TenantID, w.ID, id))
		} else {
			update := workspaceSQL(q, "UPDATE workspace_notes SET title=$1,text=$2,version=version+1,updated_at=clock_timestamp() WHERE tenant_id=$3 AND workspace_id=$4 AND id=$5", "UPDATE workspace_notes SET title=?,text=?,version=version+1,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=?", `UPDATE WORKSPACE_NOTES SET TITLE=?,TEXT=?,VERSION=VERSION+1,UPDATED_AT=CURRENT_TIMESTAMP(6) WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ID=?`, title, text, w.TenantID, w.ID, id)
			if q.Dialect() == transaction.Dameng && text == "" {
				update = transaction.SQL(transaction.Dameng, `UPDATE WORKSPACE_NOTES SET TITLE=?,TEXT=EMPTY_CLOB(),VERSION=VERSION+1,UPDATED_AT=CURRENT_TIMESTAMP(6) WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ID=?`, title, w.TenantID, w.ID, id)
			}
			affected, e = q.Exec(ctx, update)
			if e == nil && affected != 1 {
				return fail("WORKSPACE_WRITE_UNCONFIRMED")
			}
			if e == nil {
				out, e = selectNoteWithin(ctx, q, w, id, false)
			}
		}
		if e != nil {
			return e
		}
		if affected != 1 {
			return fail("WORKSPACE_WRITE_UNCONFIRMED")
		}
		return s.scopedAuditWithin(ctx, q, a, w, action, "workspace_note", id, "", "", requestID)
	})
	if e != nil {
		return Note{}, e
	}
	return out, nil
}
