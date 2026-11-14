package identity

import (
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// The DM catalog has no server UUID default. The authority identifies this
// membership lifetime; removal and re-addition must receive different values.
func identityMembershipSQL(q transaction.Executor, user, tenant, workspace, role string) transaction.Statement {
	if q.Dialect() == transaction.Dameng {
		return transaction.SQL(transaction.Dameng, "INSERT INTO WORKSPACE_MEMBERSHIPS(USER_ID,TENANT_ID,WORKSPACE_ID,ROLE,AUTHORITY_ID) VALUES(?,?,?,?,?)", user, tenant, workspace, role, uuid.NewString())
	}
	return identitySQL(q, "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,$4)", "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES(?,?,?,?)", "", user, tenant, workspace, role)
}

// JSON is text in native CLOB columns. Cryptographic hashes/ciphertext remain
// byte slices, so the driver cannot mistake JSON for VARBINARY input.
func identityJSON(q transaction.Executor, value []byte) any {
	if q.Dialect() == transaction.Dameng {
		return string(value)
	}
	return value
}
