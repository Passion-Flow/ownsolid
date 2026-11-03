package sso

import (
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const maximumBindingInventory = config.SSOMaximumBindingsPerUser
const bindingColumns = `b.id::text,b.connector_id::text,c.name,c.provider,b.subject,b.active,b.revision,b.created_at,b.updated_at`

func scanBinding(row transaction.Row) (Binding, error) {
	var b Binding
	e := row.Scan(&b.ID, &b.ConnectorID, &b.Name, &b.Provider, &b.Subject, &b.Active, &b.Revision, &b.CreatedAt, &b.UpdatedAt)
	b.CreatedAt, b.UpdatedAt = b.CreatedAt.UTC(), b.UpdatedAt.UTC()
	return b, e
}
