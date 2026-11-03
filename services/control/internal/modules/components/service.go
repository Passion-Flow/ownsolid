// Package components owns component observations, maintenance intent and bounded logs.
package components

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type Service struct {
	Nodes            *nodes.Service
	ArchiveFile      string
	CatalogDirectory string
}

func decodeConfig(raw []byte) (*wire.ComponentConfig, error) {
	if raw == nil {
		return nil, nil
	}
	var value wire.ComponentConfig
	if json.Unmarshal(raw, &value) != nil || value.Validate() != nil {
		return nil, errors.New("STORAGE_UNAVAILABLE")
	}
	return &value, nil
}
func encodeConfig(value *wire.ComponentConfig) []byte {
	if value == nil {
		return nil
	}
	encoded, _ := json.Marshal(value)
	return encoded
}
func (s *Service) Observe(ctx context.Context, tx pgx.Tx, n nodes.Node, o wire.ComponentObservation, taskID string) error {
	return s.ObserveWithin(ctx, transaction.PostgreSQLTransaction(tx), n, o, taskID)
}
func (s *Service) RecordResult(ctx context.Context, tx pgx.Tx, n nodes.Node, t nodes.Task, result wire.Result) error {
	return s.RecordResultWithin(ctx, transaction.PostgreSQLTransaction(tx), n, t, result)
}
