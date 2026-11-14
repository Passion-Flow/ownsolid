package workspaces

import (
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func membershipInsertSQL(q transaction.Executor, user, tenant, workspace, role string) transaction.Statement {
	if q.Dialect() == transaction.Dameng {
		return transaction.SQL(transaction.Dameng, "INSERT INTO WORKSPACE_MEMBERSHIPS(USER_ID,TENANT_ID,WORKSPACE_ID,ROLE,AUTHORITY_ID) VALUES(?,?,?,?,?)", user, tenant, workspace, role, uuid.NewString())
	}
	return workspaceSQL(q, "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,$4)", "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES(?,?,?,?)", "", user, tenant, workspace, role)
}
