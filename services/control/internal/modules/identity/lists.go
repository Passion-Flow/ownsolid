package identity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
)

// These bounds are existing wire/schema invariants,not adjustable deployment policy.
const maximumListLimit = 100
const maximumListCursorBytes = 512
const minimumListTimestampYear = 1000
const maximumListTimestampYear = 9999

// maximumUserSearch is the longest address, so no longer text can match.
const maximumUserSearch = 254

type cursor struct {
	Time       time.Time  `json:"time"`
	ID         string     `json:"id"`
	Filter     string     `json:"filter"`
	WindowFrom *time.Time `json:"window_from,omitempty"`
	WindowTo   *time.Time `json:"window_to,omitempty"`
}
type List struct {
	Items      []map[string]any `json:"items"`
	NextCursor *string          `json:"next_cursor"`
}

var auditActions = map[string]bool{}

// auditCategory is an action's family, the part before its first dot. Its letters hold no LIKE
// wildcard, so the filter matches exactly that family.
var auditCategory = regexp.MustCompile(`^[a-z]{1,32}$`)

func init() {
	for _, a := range strings.Fields("installation.bootstrapped invitation.created invitation.resent invitation.revoked invitation.accepted invitation.expired auth.login_succeeded auth.login_failed auth.logout auth.recovery_requested auth.password_reset account.updated account.disabled account.enabled mail.sent mail.failed mail.cancelled system.retention operator.admin_recovered operator.auth_invalidated workspace.created workspace.updated workspace.archived workspace.restored workspace.member_added workspace.member_updated workspace.member_removed workspace.note_created workspace.note_updated workspace.note_deleted node.join_token_created node.join_token_revoked node.enrolled node.certificate_renewed node.certificate_recovered node.updated node.task_requested node.task_claimed node.task_succeeded node.task_failed node.task_cancelled node.task_released node.resync_granted node.pacing_updated mail.resent") {
		auditActions[a] = true
	}
}

type listPlan struct {
	kind, alias, order, pgFrom, myFrom, pgProjection, myProjection, filter string
	dmFrom, dmProjection                                                   string
	pgWhere, myWhere                                                       []string
	dmWhere                                                                []string
	pgArgs, myArgs                                                         []any
	dmArgs                                                                 []any
	hasCursor                                                              bool
	// ascending lists oldest first; every list defaults to newest first.
	ascending            bool
	limit                int
	windowFrom, windowTo *time.Time
}

func listTime(value time.Time) bool {
	return !value.IsZero() && value.UTC().Year() >= minimumListTimestampYear && value.UTC().Year() <= maximumListTimestampYear
}
func (p *listPlan) compare(column, operator string, value any, suffix string) {
	p.pgArgs = append(p.pgArgs, value)
	p.myArgs = append(p.myArgs, value)
	p.dmArgs = append(p.dmArgs, value)
	p.pgWhere = append(p.pgWhere, column+operator+"$"+strconv.Itoa(len(p.pgArgs))+suffix)
	p.myWhere = append(p.myWhere, column+operator+"?"+suffix)
	p.dmWhere = append(p.dmWhere, column+operator+"?"+suffix)
}

// matchEither keeps rows where either column holds a LIKE pattern, whatever its case.
func (p *listPlan) matchEither(first, second, pattern string) {
	p.pgArgs = append(p.pgArgs, pattern)
	p.myArgs = append(p.myArgs, pattern, pattern)
	p.dmArgs = append(p.dmArgs, pattern, pattern)
	number := "$" + strconv.Itoa(len(p.pgArgs))
	p.pgWhere = append(p.pgWhere, "(lower("+first+") LIKE lower("+number+") ESCAPE '!' OR lower("+second+") LIKE lower("+number+") ESCAPE '!')")
	either := "(lower(" + first + ") LIKE lower(?) ESCAPE '!' OR lower(" + second + ") LIKE lower(?) ESCAPE '!')"
	p.myWhere = append(p.myWhere, either)
	p.dmWhere = append(p.dmWhere, either)
}

// decodeListCursor accepts only the bounded flat cursor contract;unknown or
// duplicate metadata cannot silently override the selected filter/window.
func decodeListCursor(raw []byte) (cursor, error) {
	var c cursor
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, e := decoder.Token()
	if e != nil || token != json.Delim('{') {
		return c, failure("INVALID_CURSOR")
	}
	seen := map[string]bool{}
	for decoder.More() {
		token, e = decoder.Token()
		if e != nil {
			return c, failure("INVALID_CURSOR")
		}
		key, ok := token.(string)
		if !ok || seen[key] || key != "time" && key != "id" && key != "filter" && key != "window_from" && key != "window_to" {
			return c, failure("INVALID_CURSOR")
		}
		seen[key] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return c, failure("INVALID_CURSOR")
		}
	}
	token, e = decoder.Token()
	if e != nil || token != json.Delim('}') {
		return c, failure("INVALID_CURSOR")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || json.Unmarshal(raw, &c) != nil {
		return cursor{}, failure("INVALID_CURSOR")
	}
	return c, nil
}
func makeListPlan(kind string, q url.Values, policy config.RuntimePolicy, now time.Time) (listPlan, error) {
	p := listPlan{kind: kind, limit: policy.UI.PageSize, order: "created_at", pgWhere: []string{"true"}, myWhere: []string{"true"}, dmWhere: []string{"1=1"}}
	fail := func(code string) (listPlan, error) { return listPlan{}, failure(code) }
	if p.limit < 1 || p.limit > maximumListLimit {
		return fail("CONFIG_UI_BOUNDS_INVALID")
	}
	fingerprint := url.Values{}
	for k, v := range q {
		if len(v) != 1 {
			return fail("VALIDATION_FAILED")
		}
		if k != "limit" && k != "cursor" {
			fingerprint[k] = v
		}
	}
	if v := q.Get("limit"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 || n > maximumListLimit {
			return fail("VALIDATION_FAILED")
		}
		p.limit = n
	}
	sum := sha256.Sum256([]byte(kind + ":" + fingerprint.Encode()))
	p.filter = hex.EncodeToString(sum[:])
	var c cursor
	if raw := q.Get("cursor"); raw != "" {
		if len(raw) > maximumListCursorBytes {
			return fail("INVALID_CURSOR")
		}
		decoded, e := base64.RawURLEncoding.DecodeString(raw)
		if e != nil || base64.RawURLEncoding.EncodeToString(decoded) != raw {
			return fail("INVALID_CURSOR")
		}
		c, e = decodeListCursor(decoded)
		if e != nil || c.Filter != p.filter || !sessionID(c.ID) || !listTime(c.Time) {
			return fail("INVALID_CURSOR")
		}
	}
	allowed := map[string]bool{"limit": true, "cursor": true}
	enum := func(key, choices string) error {
		allowed[key] = true
		if v := q.Get(key); v != "" {
			valid := false
			for _, choice := range strings.Split(choices, "|") {
				if choice == v {
					valid = true
				}
			}
			if !valid {
				return failure("VALIDATION_FAILED")
			}
			p.compare(p.alias+"."+key, "=", v, "")
		}
		return nil
	}
	// emailPrefix filters on the start of an address in a lowercase column expression:
	// printable ASCII only, with LIKE's wildcards escaped.
	emailPrefix := func(key, column string) error {
		allowed[key] = true
		values, ok := q[key]
		if !ok {
			return nil
		}
		prefix := values[0]
		if len(prefix) < 1 || len(prefix) > 254 {
			return failure("VALIDATION_FAILED")
		}
		for _, r := range prefix {
			if r >= 127 || r < 32 {
				return failure("VALIDATION_FAILED")
			}
		}
		p.compare(column, " LIKE ", pagination.EscapeLike(strings.ToLower(prefix))+"%", " ESCAPE '!'")
		return nil
	}
	switch kind {
	case "users":
		p.dmFrom = "USERS u"
		p.dmProjection = "u.ID,u.EMAIL_DISPLAY,u.DISPLAY_NAME,u.LOCALE,u.STATUS,u.IS_PLATFORM_ADMIN,u.EMAIL_VERIFIED_AT,u.VERSION,u.CREATED_AT,u.UPDATED_AT,u.AUTH_EPOCH"
		p.alias, p.pgFrom, p.myFrom = "u", "users u", "users u"
		p.pgProjection = "u.id::text,u.email_display,u.display_name,u.locale,u.status,u.is_platform_admin,u.email_verified_at,u.version,u.created_at,u.updated_at,u.auth_epoch"
		p.myProjection = "u.id,u.email_display,u.display_name,u.locale,u.status,u.is_platform_admin,u.email_verified_at,u.version,u.created_at,u.updated_at,u.auth_epoch"
		if e := enum("status", "active|disabled"); e != nil {
			return listPlan{}, e
		}
		if e := emailPrefix("email_prefix", "u.email_canonical"); e != nil {
			return listPlan{}, e
		}
		allowed[pagination.ListSearchParameter] = true
		if values, ok := q[pagination.ListSearchParameter]; ok {
			if !pagination.SearchTerm(values[0], maximumUserSearch) {
				return listPlan{}, failure("VALIDATION_FAILED")
			}
			p.matchEither("u.email_canonical", "u.display_name", pagination.ContainsPattern(values[0]))
		}
	case "invitations":
		p.dmFrom = "INVITATIONS i LEFT JOIN MAIL_DELIVERIES d ON d.ID=(SELECT L.ID FROM MAIL_DELIVERIES L WHERE L.INVITATION_ID=i.ID ORDER BY L.CREATED_AT DESC,L.ID DESC LIMIT 1)"
		p.dmProjection = "i.ID,i.EMAIL_CANONICAL,i.LOCALE,CASE WHEN i.STATUS='pending' AND i.EXPIRES_AT<=CURRENT_TIMESTAMP(6) THEN 'expired' ELSE i.STATUS END,i.VERSION,i.EXPIRES_AT,i.CREATED_AT,i.UPDATED_AT,d.ID,d.STATE,d.ATTEMPT_COUNT,d.ERROR_CATEGORY"
		p.alias = "i"
		p.pgFrom = "invitations i LEFT JOIN LATERAL (SELECT id,state,attempt_count,error_category FROM mail_deliveries WHERE invitation_id=i.id ORDER BY created_at DESC,id DESC LIMIT 1) d ON true"
		p.myFrom = "invitations i LEFT JOIN mail_deliveries d ON d.id=(SELECT latest.id FROM mail_deliveries latest WHERE latest.invitation_id=i.id ORDER BY latest.created_at DESC,latest.id DESC LIMIT 1)"
		p.pgProjection = "i.id::text,i.email_canonical,i.locale,CASE WHEN i.status='pending' AND i.expires_at<=clock_timestamp() THEN 'expired' ELSE i.status END,i.version,i.expires_at,i.created_at,i.updated_at,d.id::text,d.state,d.attempt_count,d.error_category"
		p.myProjection = "i.id,i.email_canonical,i.locale,CASE WHEN i.status='pending' AND i.expires_at<=UTC_TIMESTAMP(6) THEN 'expired' ELSE i.status END,i.version,i.expires_at,i.created_at,i.updated_at,d.id,d.state,d.attempt_count,d.error_category"
		allowed["status"] = true
		if v := q.Get("status"); v != "" {
			switch v {
			case "pending":
				p.dmWhere = append(p.dmWhere, "i.STATUS='pending' AND i.EXPIRES_AT>CURRENT_TIMESTAMP(6)")
				p.pgWhere = append(p.pgWhere, "i.status='pending' AND i.expires_at>clock_timestamp()")
				p.myWhere = append(p.myWhere, "i.status='pending' AND i.expires_at>UTC_TIMESTAMP(6)")
			case "expired":
				p.dmWhere = append(p.dmWhere, "(i.STATUS='expired' OR(i.STATUS='pending' AND i.EXPIRES_AT<=CURRENT_TIMESTAMP(6)))")
				p.pgWhere = append(p.pgWhere, "(i.status='expired' OR(i.status='pending' AND i.expires_at<=clock_timestamp()))")
				p.myWhere = append(p.myWhere, "(i.status='expired' OR(i.status='pending' AND i.expires_at<=UTC_TIMESTAMP(6)))")
			case "accepted", "revoked":
				p.compare("i.status", "=", v, "")
			default:
				return fail("VALIDATION_FAILED")
			}
		}
	case "mail-deliveries":
		p.dmFrom = "MAIL_DELIVERIES d LEFT JOIN INVITATIONS v ON v.ID=d.INVITATION_ID"
		p.dmProjection = "d.ID,d.PURPOSE,d.RECIPIENT_EMAIL,d.LOCALE,d.STATE,d.ATTEMPT_COUNT,CASE WHEN d.STATE='queued' THEN d.NEXT_ATTEMPT_AT ELSE NULL END,d.ERROR_CATEGORY,d.CREATED_AT,d.UPDATED_AT,d.COMPLETED_AT,v.ID,v.VERSION,CASE WHEN v.STATUS='pending' AND v.EXPIRES_AT<=CURRENT_TIMESTAMP(6) THEN 'expired' ELSE v.STATUS END"
		p.alias, p.pgFrom, p.myFrom = "d", "mail_deliveries d LEFT JOIN invitations v ON v.id=d.invitation_id", "mail_deliveries d LEFT JOIN invitations v ON v.id=d.invitation_id"
		p.pgProjection = "d.id::text,d.purpose,d.recipient_email,d.locale,d.state,d.attempt_count,CASE WHEN d.state='queued' THEN d.next_attempt_at ELSE NULL END,d.error_category,d.created_at,d.updated_at,d.completed_at,v.id::text,v.version,CASE WHEN v.status='pending' AND v.expires_at<=clock_timestamp() THEN 'expired' ELSE v.status END"
		p.myProjection = "d.id,d.purpose,d.recipient_email,d.locale,d.state,d.attempt_count,CASE WHEN d.state='queued' THEN d.next_attempt_at ELSE NULL END,d.error_category,d.created_at,d.updated_at,d.completed_at,v.id,v.version,CASE WHEN v.status='pending' AND v.expires_at<=UTC_TIMESTAMP(6) THEN 'expired' ELSE v.status END"
		if e := enum("state", "queued|sending|sent|failed|cancelled"); e != nil {
			return listPlan{}, e
		}
		if e := enum("purpose", "invitation|password_reset|password_changed"); e != nil {
			return listPlan{}, e
		}
		if e := emailPrefix("recipient_prefix", "lower(d.recipient_email)"); e != nil {
			return listPlan{}, e
		}
		allowed["order"] = true
		switch q.Get("order") {
		case "", "newest":
		case "oldest":
			p.ascending = true
		default:
			return fail("VALIDATION_FAILED")
		}
	case "audit-events":
		p.dmFrom = "AUDIT_EVENTS a LEFT JOIN USERS actor_user ON actor_user.ID=a.ACTOR_USER_ID LEFT JOIN USERS subject_user ON subject_user.ID=a.SUBJECT_USER_ID"
		p.dmProjection = "a.ID,a.OCCURRED_AT,a.ACTOR_USER_ID,actor_user.EMAIL_DISPLAY,a.SUBJECT_USER_ID,subject_user.EMAIL_DISPLAY,a.AUDIENCE,a.ACTION,a.OUTCOME,a.TARGET_TYPE,a.TARGET_ID,a.REASON,a.REQUEST_ID"
		const auditJoins = " LEFT JOIN users actor_user ON actor_user.id=a.actor_user_id LEFT JOIN users subject_user ON subject_user.id=a.subject_user_id"
		p.alias, p.order, p.pgFrom, p.myFrom = "a", "occurred_at", "audit_events a"+auditJoins, "audit_events a"+auditJoins
		p.pgProjection = "a.id::text,a.occurred_at,a.actor_user_id::text,actor_user.email_display,a.subject_user_id::text,subject_user.email_display,a.audience,a.action,a.outcome,a.target_type,a.target_id::text,a.reason,a.request_id::text"
		p.myProjection = "a.id,a.occurred_at,a.actor_user_id,actor_user.email_display,a.subject_user_id,subject_user.email_display,a.audience,a.action,a.outcome,a.target_type,a.target_id,a.reason,a.request_id"
		if e := enum("outcome", "success|failure|accepted"); e != nil {
			return listPlan{}, e
		}
		allowed["action"] = true
		if v := q.Get("action"); v != "" {
			if !auditActions[v] {
				return fail("VALIDATION_FAILED")
			}
			p.compare("a.action", "=", v, "")
		}
		allowed["target_id"] = true
		if v := q.Get("target_id"); v != "" {
			if !sessionID(v) {
				return fail("VALIDATION_FAILED")
			}
			p.compare("a.target_id", "=", v, "")
		}
		allowed["category"], allowed["actor_user_id"] = true, true
		if v := q.Get("category"); v != "" {
			if !auditCategory.MatchString(v) {
				return fail("VALIDATION_FAILED")
			}
			p.compare("a.action", " LIKE ", v+".%", "")
		}
		if v := q.Get("actor_user_id"); v != "" {
			if !sessionID(v) {
				return fail("VALIDATION_FAILED")
			}
			p.compare("a.actor_user_id", "=", v, "")
		}
		allowed["from"], allowed["to"], allowed["request_id"] = true, true, true
		from, to := now.UTC().Add(-time.Duration(policy.Identity.AuditDefaultDays)*24*time.Hour), now.UTC()
		for _, b := range []struct {
			key   string
			value *time.Time
		}{{"from", &from}, {"to", &to}} {
			if v := q.Get(b.key); v != "" {
				parsed, e := time.Parse(time.RFC3339, v)
				if e != nil || !listTime(parsed) {
					return fail("VALIDATION_FAILED")
				}
				*b.value = parsed.UTC()
			}
		}
		if c.ID != "" {
			if (c.WindowFrom == nil) != (c.WindowTo == nil) {
				return fail("INVALID_CURSOR")
			}
			if c.WindowFrom != nil {
				if !listTime(*c.WindowFrom) || !listTime(*c.WindowTo) || q.Get("from") != "" && !from.Equal(*c.WindowFrom) || q.Get("to") != "" && !to.Equal(*c.WindowTo) {
					return fail("INVALID_CURSOR")
				}
				from, to = c.WindowFrom.UTC(), c.WindowTo.UTC()
			} else if q.Get("from") == "" || q.Get("to") == "" {
				return fail("INVALID_CURSOR")
			}
		}
		if !listTime(from) || !listTime(to) || !from.Before(to) || to.Sub(from) > time.Duration(policy.Identity.AuditMaxDays)*24*time.Hour {
			return fail("VALIDATION_FAILED")
		}
		p.windowFrom, p.windowTo = &from, &to
		p.compare("a.occurred_at", ">=", from, "")
		p.compare("a.occurred_at", "<=", to, "")
		if v := q.Get("request_id"); v != "" {
			if !sessionID(v) {
				return fail("VALIDATION_FAILED")
			}
			p.compare("a.request_id", "=", v, "")
		}
	default:
		return fail("RESOURCE_NOT_FOUND")
	}
	for key := range q {
		if !allowed[key] {
			return fail("VALIDATION_FAILED")
		}
	}
	if kind != "audit-events" && (c.WindowFrom != nil || c.WindowTo != nil) {
		return fail("INVALID_CURSOR")
	}
	if c.ID != "" {
		p.hasCursor = true
		column, after := p.alias+"."+p.order, "<"
		if p.ascending {
			after = ">"
		}
		p.pgArgs = append(p.pgArgs, c.Time, c.ID)
		n := len(p.pgArgs)
		p.pgWhere = append(p.pgWhere, "("+column+","+p.alias+".id)"+after+"($"+strconv.Itoa(n-1)+",$"+strconv.Itoa(n)+"::uuid)")
		p.myArgs = append(p.myArgs, c.Time, c.Time, c.ID)
		p.myWhere = append(p.myWhere, "("+column+after+"? OR("+column+"=? AND "+p.alias+".id"+after+"?))")
		p.dmArgs = append(p.dmArgs, c.Time.UTC(), c.Time.UTC(), c.ID)
		p.dmWhere = append(p.dmWhere, "("+column+after+"? OR("+column+"=? AND "+p.alias+".id"+after+"?))")
	}
	return p, nil
}
func (p listPlan) statement(dialect transaction.Dialect) transaction.Statement {
	direction := " DESC"
	if p.ascending {
		direction = " ASC"
	}
	order := " ORDER BY " + p.alias + "." + p.order + direction + "," + p.alias + ".id" + direction + " LIMIT "
	if dialect == transaction.Dameng {
		if p.limit < 1 || p.limit > maximumListLimit || p.dmFrom == "" {
			return transaction.SQL(transaction.Dameng, "")
		}
		// Only this already bounded integer is embedded. Filters and cursor
		// values remain parameters; no vendor query is translated.
		return transaction.SQL(transaction.Dameng, "SELECT "+p.dmProjection+" FROM "+p.dmFrom+" WHERE "+strings.Join(p.dmWhere, " AND ")+order+strconv.Itoa(p.limit+1), p.dmArgs...)
	}
	if dialect == transaction.MySQL {
		args := append(append([]any{}, p.myArgs...), p.limit+1)
		return transaction.SQL(dialect, "SELECT "+p.myProjection+" FROM "+p.myFrom+" WHERE "+strings.Join(p.myWhere, " AND ")+order+"?", args...)
	}
	if dialect != transaction.PostgreSQL {
		return transaction.SQL(dialect, "")
	}
	args := append(append([]any{}, p.pgArgs...), p.limit+1)
	return transaction.SQL(dialect, "SELECT "+p.pgProjection+" FROM "+p.pgFrom+" WHERE "+strings.Join(p.pgWhere, " AND ")+order+"$"+strconv.Itoa(len(args)), args...)
}

// List is a trusted repository read retained for internal callers. HTTP uses ListForActor.
func (s *Service) List(ctx context.Context, kind string, q url.Values) (List, error) {
	p, e := makeListPlan(kind, q, s.Config.Policy(), time.Now().UTC().Truncate(time.Microsecond))
	if e != nil {
		return List{Items: []map[string]any{}}, e
	}
	store, e := s.Repository()
	if e != nil {
		return List{Items: []map[string]any{}}, e
	}
	ctx, cancel := s.sessionContext(ctx)
	defer cancel()
	out, e := readList(ctx, store, p)
	return out, sessionError(e)
}
func (s *Service) ListForActor(ctx context.Context, a Auth, kind string, q url.Values) (List, error) {
	empty := List{Items: []map[string]any{}}
	if !sessionActor(a) {
		return empty, failure("AUTH_SESSION_EXPIRED")
	}
	if a.Me.Audience != "admin" {
		return empty, failure("ACCESS_DENIED")
	}
	p, e := makeListPlan(kind, q, s.Config.Policy(), time.Now().UTC().Truncate(time.Microsecond))
	if e != nil {
		return empty, e
	}
	store, e := s.Repository()
	if e != nil {
		return empty, e
	}
	ctx, cancel := s.sessionContext(ctx)
	defer cancel()
	var out List
	e = store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		if e := s.RecheckWithin(ctx, tx, a); e != nil {
			return e
		}
		var e error
		out, e = readList(ctx, tx, p)
		return e
	})
	if e != nil {
		return empty, sessionError(e)
	}
	return out, nil
}
func readList(ctx context.Context, q transaction.Executor, p listPlan) (List, error) {
	empty := List{Items: []map[string]any{}}
	if q.Dialect() == transaction.Dameng {
		if e := p.validateDamengTimes(); e != nil {
			return empty, e
		}
	}
	out := empty
	rows, e := q.Query(ctx, p.statement(q.Dialect()))
	if e != nil {
		return empty, e
	}
	defer rows.Close()
	var lastTime time.Time
	var lastID string
	for rows.Next() {
		item, stamp, id, e := scanListRow(p.kind, rows)
		if e != nil {
			return empty, e
		}
		out.Items = append(out.Items, item)
		if len(out.Items) == p.limit {
			lastTime, lastID = stamp, id
		}
		if len(out.Items) > p.limit+1 {
			return empty, failure("STORAGE_UNAVAILABLE")
		}
	}
	if e = rows.Err(); e != nil {
		return empty, e
	}
	if len(out.Items) > p.limit {
		out.Items = out.Items[:p.limit]
		raw, e := json.Marshal(cursor{lastTime.UTC(), lastID, p.filter, p.windowFrom, p.windowTo})
		if e != nil || len(base64.RawURLEncoding.EncodeToString(raw)) > maximumListCursorBytes {
			return empty, failure("STORAGE_UNAVAILABLE")
		}
		value := base64.RawURLEncoding.EncodeToString(raw)
		out.NextCursor = &value
	}
	return out, nil
}

func (p listPlan) validateDamengTimes() error {
	for _, value := range p.dmArgs {
		if stamp, ok := value.(time.Time); ok {
			_, offset := stamp.Zone()
			if stamp.Year() < 1970 || stamp.Year() > 9999 || stamp.Nanosecond()%1000 != 0 || offset != 0 {
				if p.hasCursor {
					return failure("INVALID_CURSOR")
				}
				return failure("VALIDATION_FAILED")
			}
		}
	}
	return nil
}
