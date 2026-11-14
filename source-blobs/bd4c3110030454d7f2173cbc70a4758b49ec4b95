package databaseaccess

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/mysqloperator"
)

// MySQLRuntimeBinding is available only after actual current role/login/ledger/
// audit verification. It carries no secret and is not a PostgreSQL attestation.
func MySQLRuntimeBinding(ctx context.Context, o *mysqloperator.Context, i MySQLRuntimeInput) (string, error) {
	status, e := CheckMySQL(ctx, o, i)
	if e != nil {
		return "", e
	}
	if status != Verified {
		return "", errors.New("BOOTSTRAP_RUNTIME_ACCESS_NOT_VERIFIED")
	}
	target, e := o.Binding(ctx)
	if e != nil {
		return "", e
	}
	if e = i.verify(); e != nil {
		return "", e
	}
	raw, e := json.Marshal(struct {
		Target string `json:"target_sha256"`
		Input  string `json:"runtime_input_sha256"`
	}{target, i.fingerprint})
	if e != nil {
		return "", errors.New("MYSQL_RUNTIME_ACCESS_BINDING_INVALID")
	}
	return mysqloperator.SHA256(raw), nil
}
