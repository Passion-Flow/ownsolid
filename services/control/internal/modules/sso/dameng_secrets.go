package sso

import (
	"context"
	"strconv"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
)

// Native eager result materialization is capped at 10,000 rows. Scan encrypted
// startup inventory in keyset pages under the existing catalog page capacity;
// retain independent total caps and close every page before the next query.
// No raw secret or successful payload leaves a failing page/startup operation.
func (s *Service) verifyDamengStoredSecrets(ctx context.Context, q transaction.Executor) error {
	var now time.Time
	if e := q.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT CURRENT_TIMESTAMP(6) FROM DUAL")).Scan(&now); e != nil || !damengTimestamp(now) {
		return fail("STORAGE_UNAVAILABLE")
	}
	for _, inventory := range []struct {
		purpose string
		maximum int
	}{
		{clientSecretPurpose, config.SSOMaximumConnectors},
		{flowVerifierPurpose, config.SSOMaximumPendingFlows},
	} {
		last, total := "", 0
		for {
			// The one additional row is a refusal sentinel, never an accepted
			// authority. Both values are fixed bounded configuration contracts.
			limit := min(pagination.MaximumLimit, inventory.maximum-total+1)
			if limit < 1 {
				return fail("SSO_CAPACITY_EXCEEDED")
			}
			var text string
			var args []any
			if inventory.purpose == clientSecretPurpose {
				text = "SELECT C.TENANT_ID,C.ID,C.ID,C.SECRET_KEY_ID,C.SECRET_NONCE,C.SECRET_CIPHERTEXT FROM SSO_CONNECTORS C JOIN INSTALLATION_STATE I ON I.TENANT_ID=C.TENANT_ID WHERE I.ID=1 AND I.BOOTSTRAP_COMPLETE=1"
				if last != "" {
					text += " AND C.ID>?"
					args = append(args, last)
				}
				text += " ORDER BY C.ID LIMIT "
			} else {
				text = "SELECT F.TENANT_ID,F.CONNECTOR_ID,F.ID,F.VERIFIER_KEY_ID,F.VERIFIER_NONCE,F.VERIFIER_CIPHERTEXT FROM SSO_FLOWS F JOIN INSTALLATION_STATE I ON I.TENANT_ID=F.TENANT_ID WHERE I.ID=1 AND I.BOOTSTRAP_COMPLETE=1 AND F.STATE='pending' AND F.EXPIRES_AT>?"
				args = append(args, now)
				if last != "" {
					text += " AND F.ID>?"
					args = append(args, last)
				}
				text += " ORDER BY F.ID LIMIT "
			}
			rows, e := q.Query(ctx, transaction.SQL(transaction.Dameng, text+strconv.Itoa(limit), args...))
			if e != nil {
				return fail("STORAGE_UNAVAILABLE")
			}
			read, next, e := s.verifyDamengSecretPage(rows, inventory.purpose, last, limit, inventory.maximum-total)
			rows.Close()
			if e != nil {
				return e
			}
			total, last = total+read, next
			if read < limit {
				break
			}
		}
	}
	return nil
}

func (s *Service) verifyDamengSecretPage(rows transaction.Rows, purpose, last string, limit, remaining int) (int, string, error) {
	read := 0
	for rows.Next() {
		if read >= limit || read >= remaining {
			return 0, "", fail("SSO_CAPACITY_EXCEEDED")
		}
		var scope secretScope
		var sealed sealedSecret
		scope.purpose = purpose
		if e := rows.Scan(&scope.tenant, &scope.connector, &scope.record, &sealed.keyID, &sealed.nonce, &sealed.ciphertext); e != nil {
			return 0, "", fail("STORAGE_UNAVAILABLE")
		}
		if !canonicalID(scope.record) || last != "" && scope.record <= last {
			return 0, "", fail("STORAGE_UNAVAILABLE")
		}
		if e := s.verifyStoredSecret(scope, sealed); e != nil {
			return 0, "", e
		}
		last = scope.record
		read++
	}
	if rows.Err() != nil {
		return 0, "", fail("STORAGE_UNAVAILABLE")
	}
	return read, last, nil
}
