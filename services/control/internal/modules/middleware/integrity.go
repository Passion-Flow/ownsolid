package middleware

import (
	"context"
	"encoding/json"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// VerifyStored checks owning data invariants, not DDL/trigger metadata acceptance.
// It never imports a backup or adopts/replays an uncertain operation.
func (s *Service) VerifyStored(ctx context.Context) error {
	store, e := s.repository()
	if e != nil {
		return e
	}
	if s.configuration.Validate() != nil {
		return fail("MIDDLEWARE_CONFIGURATION_INVALID")
	}
	for _, p := range s.configuration.Profiles {
		if _, e := Capture(p); e != nil {
			return e
		}
	}
	ctx, cancel := s.bounded(ctx)
	defer cancel()
	return store.Within(ctx, true, func(ctx context.Context, q transaction.Executor) error {
		var invalid int
		if e := q.QueryRow(ctx, statement(q, `SELECT count(*) FROM middleware_instances WHERE state NOT IN('active','archived') OR version NOT BETWEEN 1 AND 9007199254740991`, `SELECT count(*) FROM middleware_instances WHERE state NOT IN('active','archived') OR version NOT BETWEEN 1 AND 9007199254740991`)).Scan(&invalid); e != nil {
			return e
		}
		if invalid != 0 {
			return fail("MIDDLEWARE_STORED_INSTANCE_INVALID")
		}
		cursor := ""
		for {
			rows, e := q.Query(ctx, statement(q, "SELECT "+workPG+" FROM middleware_operations WHERE id::text>$1 ORDER BY id LIMIT $2", "SELECT "+workMy+" FROM middleware_operations WHERE id>? ORDER BY id LIMIT ?", cursor, s.configuration.Policy.PageSize))
			if e != nil {
				return e
			}
			batch := []*claimed{}
			for rows.Next() {
				c, e := scanClaim(rows)
				if e != nil {
					rows.Close()
					return e
				}
				batch = append(batch, c)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return e
			}
			if len(batch) == 0 {
				return nil
			}
			for _, c := range batch {
				var result, digest *string
				e = q.QueryRow(ctx, statement(q, `SELECT CASE WHEN octet_length(result_json)<=$1 THEN result_json ELSE NULL END,result_sha256 FROM middleware_operations WHERE id=$2`, `SELECT CASE WHEN OCTET_LENGTH(result_json)<=? THEN result_json ELSE NULL END,result_sha256 FROM middleware_operations WHERE id=?`, s.configuration.Policy.MaximumEvidenceBytes, c.ID)).Scan(&result, &digest)
				if e != nil {
					return e
				}
				if c.State == "succeeded" {
					var evidence executionEvidence
					if result == nil || digest == nil || hash([]byte(*result)) != *digest || wire.StrictJSON([]byte(*result), &evidence) != nil || evidence.FormatVersion != 1 || evidence.Kind != "health" && evidence.Kind != "backup" && evidence.Kind != "isolated_restore" {
						return fail("MIDDLEWARE_STORED_EVIDENCE_INVALID")
					}
					if evidence.Kind == "health" {
						var members map[string]json.RawMessage
						if json.Unmarshal([]byte(*result), &members) != nil {
							return fail("MIDDLEWARE_STORED_EVIDENCE_INVALID")
						}
						_, stagesPresent := members["stages"]
						if (evidence.Observation == nil) == (evidence.MySQLObservation == nil) || stagesPresent {
							return fail("MIDDLEWARE_STORED_EVIDENCE_INVALID")
						}
						if evidence.MySQLObservation != nil && !evidence.MySQLObservation.valid() {
							return fail("MIDDLEWARE_STORED_EVIDENCE_INVALID")
						}
					} else {
						return fail("MIDDLEWARE_STORED_EVIDENCE_INVALID")
					}
					if evidence.Kind != c.Kind {
						return fail("MIDDLEWARE_STORED_EVIDENCE_INVALID")
					}
					if !json.Valid([]byte(*result)) {
						return fail("MIDDLEWARE_STORED_EVIDENCE_INVALID")
					}
				} else if result != nil || digest != nil {
					return fail("MIDDLEWARE_STORED_EVIDENCE_INVALID")
				}
				cursor = c.ID
			}
		}
	})
}
