package identity

import (
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func listStamp(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }
func optionalListStamp(value *time.Time) any {
	if value == nil {
		return nil
	}
	return listStamp(*value)
}
func optionalListString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

// scanListRow projects only explicitly public fields;no encrypted/token/credential
// column is converted into a map or returned through the generic list envelope.
func scanListRow(kind string, row transaction.Row) (map[string]any, time.Time, string, error) {
	switch kind {
	case "users":
		u, e := scanUser(row)
		if e != nil {
			return nil, time.Time{}, "", e
		}
		return map[string]any{"id": u.ID, "email": u.Email, "display_name": u.DisplayName, "locale": u.Locale, "status": u.Status, "is_platform_admin": u.IsPlatformAdmin, "email_verified_at": optionalListStamp(u.EmailVerifiedAt), "version": u.Version, "created_at": listStamp(u.CreatedAt), "updated_at": listStamp(u.UpdatedAt)}, u.CreatedAt, u.ID, nil
	case "invitations":
		var id, email, locale, status string
		var version int64
		var expires, created, updated time.Time
		var deliveryID, deliveryState, category *string
		var attempts *int
		e := row.Scan(&id, &email, &locale, &status, &version, &expires, &created, &updated, &deliveryID, &deliveryState, &attempts, &category)
		if e != nil {
			return nil, time.Time{}, "", e
		}
		var delivery any
		if deliveryID != nil {
			if deliveryState == nil || attempts == nil {
				return nil, time.Time{}, "", failure("STORAGE_UNAVAILABLE")
			}
			delivery = map[string]any{"id": *deliveryID, "state": *deliveryState, "attempt_count": *attempts, "error_category": optionalListString(category)}
		}
		return map[string]any{"id": id, "email": email, "locale": locale, "status": status, "version": version, "expires_at": listStamp(expires), "created_at": listStamp(created), "updated_at": listStamp(updated), "delivery": delivery}, created, id, nil
	case "mail-deliveries":
		var id, purpose, recipient, locale, state string
		var attempts int
		var next, completed *time.Time
		var category *string
		var created, updated time.Time
		e := row.Scan(&id, &purpose, &recipient, &locale, &state, &attempts, &next, &category, &created, &updated, &completed)
		if e != nil {
			return nil, time.Time{}, "", e
		}
		return map[string]any{"id": id, "purpose": purpose, "recipient_email": recipient, "locale": locale, "state": state, "attempt_count": attempts, "next_attempt_at": optionalListStamp(next), "error_category": optionalListString(category), "created_at": listStamp(created), "updated_at": listStamp(updated), "completed_at": optionalListStamp(completed)}, created, id, nil
	case "audit-events":
		var id, audience, action, outcome, request string
		var occurred time.Time
		var actor, subject, targetType, target, reason *string
		e := row.Scan(&id, &occurred, &actor, &subject, &audience, &action, &outcome, &targetType, &target, &reason, &request)
		if e != nil {
			return nil, time.Time{}, "", e
		}
		return map[string]any{"id": id, "occurred_at": listStamp(occurred), "actor_user_id": optionalListString(actor), "subject_user_id": optionalListString(subject), "audience": audience, "action": action, "outcome": outcome, "target_type": optionalListString(targetType), "target_id": optionalListString(target), "reason": optionalListString(reason), "request_id": request}, occurred, id, nil
	}
	return nil, time.Time{}, "", failure("RESOURCE_NOT_FOUND")
}
