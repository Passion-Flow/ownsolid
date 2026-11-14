package boundary

import (
	"context"
	"crypto/subtle"
	"html/template"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
)

var loginPage = template.Must(template.New("login").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Private protocol fixture</title></head><body><h1>Sign in to the private fixture</h1><form method="post" action="/login"><input type="hidden" name="id" value="{{.ID}}"><input type="hidden" name="csrf" value="{{.CSRF}}"><label>Username <input name="username" autocomplete="username" required></label><label>Password <input type="password" name="password" autocomplete="current-password" required></label><button type="submit">Sign in</button></form></body></html>`))

func (s *sdkStore) callbackSession(ctx context.Context, q queryer, r *authRecord) error {
	provided, ok := ctx.Value(sessionContextKey{}).([]byte)
	if !ok || len(provided) != 32 {
		return errAuthority
	}
	var stored []byte
	if err := q.QueryRow(ctx, `SELECT token_digest FROM op_fixture_sessions WHERE tenant_id=$1 AND id=$2 AND user_id=$3 AND revoked_at IS NULL AND expires_at>clock_timestamp()`, s.Tenant, r.Session, r.User).Scan(&stored); err != nil || subtle.ConstantTimeCompare(provided, stored) != 1 {
		return errAuthority
	}
	return nil
}
func (f *fixtureProvider) login(w http.ResponseWriter, r *http.Request) {
	browserHash, err := browser(r.Context())
	if err != nil {
		protocolError(w, 400, "invalid_request")
		return
	}
	if r.Method == "GET" {
		values, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(values) != 1 || len(values["id"]) != 1 || !validRequestID(values.Get("id")) {
			protocolError(w, 400, "invalid_request")
			return
		}
		id := values.Get("id")
		request, err := f.store.AuthRequestByID(r.Context(), id)
		if err != nil {
			protocolError(w, 400, "invalid_request")
			return
		}
		if request.Done() {
			http.Redirect(w, r, f.store.Origin+"/authorize/callback?id="+url.QueryEscape(id), 303)
			return
		}
		csrf, err := tokenRandom()
		if err != nil {
			protocolError(w, 500, "server_error")
			return
		}
		result, err := f.store.pool.Exec(r.Context(), `UPDATE op_fixture_requests SET csrf_digest=$4 WHERE tenant_id=$1 AND id=$2 AND browser_digest=$3 AND approved_at IS NULL AND expires_at>clock_timestamp()`, f.store.Tenant, id, browserHash, digest(csrf))
		if err != nil || result.RowsAffected() != 1 {
			protocolError(w, 400, "invalid_request")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if loginPage.Execute(w, struct{ ID, CSRF string }{id, csrf}) != nil {
			return
		}
		return
	}
	origin, uniqueOrigin := header(r, "Origin")
	if r.Method != "POST" || !uniqueOrigin || origin != f.store.Origin {
		protocolError(w, 403, "access_denied")
		return
	}
	fields, err := boundedForm(r, f.store.Policy.Request.MaxBody)
	if err != nil || len(fields) != 4 {
		protocolError(w, 400, "invalid_request")
		return
	}
	for name := range fields {
		switch name {
		case "id", "csrf", "username", "password":
		default:
			protocolError(w, 400, "invalid_request")
			return
		}
	}
	id, csrf, username, password := fields.Get("id"), fields.Get("csrf"), fields.Get("username"), fields.Get("password")
	if !validRequestID(id) || len(csrf) != 43 || !printable(username, 1, 128, false) || !printable(password, 1, f.store.Policy.Request.MaxSecret, true) {
		protocolError(w, 400, "invalid_request")
		return
	}
	tx, err := f.store.pool.Begin(r.Context())
	if err != nil {
		protocolError(w, 503, "server_error")
		return
	}
	defer rollback(tx, f.store.Policy.Operation)
	request, err := authScan(tx.QueryRow(r.Context(), `SELECT `+authColumns+` FROM op_fixture_requests WHERE tenant_id=$1 AND id=$2 AND browser_digest=$3 AND csrf_digest=$4 AND approved_at IS NULL AND expires_at>clock_timestamp()`, f.store.Tenant, id, browserHash, digest(csrf)))
	if err != nil {
		protocolError(w, 403, "access_denied")
		return
	}
	client, _, err := f.store.registered(r.Context(), tx, request.Client, " FOR SHARE OF c,i,t")
	if err != nil || client.Revision != request.ClientRevision || client.InstallationRevision != request.InstallationRevision || client.Workspace != request.Workspace || client.Installation != request.Installation || client.Redirect != request.Redirect {
		protocolError(w, 400, "invalid_request")
		return
	}
	var epoch int64
	var subject string
	var passwordHash []byte
	err = tx.QueryRow(r.Context(), `SELECT auth_epoch,subject,password_digest FROM op_fixture_principals WHERE tenant_id=$1 AND user_id=$2 AND enabled FOR SHARE`, f.store.Tenant, f.loginUser).Scan(&epoch, &subject, &passwordHash)
	if err != nil || username != f.loginName || subtle.ConstantTimeCompare(passwordHash, digest(password)) != 1 {
		protocolError(w, 401, "access_denied")
		return
	}
	var membership string
	if err = tx.QueryRow(r.Context(), `SELECT authority_id::text FROM op_fixture_memberships WHERE tenant_id=$1 AND workspace_id=$2 AND user_id=$3 FOR SHARE`, f.store.Tenant, client.Workspace, f.loginUser).Scan(&membership); err != nil {
		protocolError(w, 403, "access_denied")
		return
	}
	var locked string
	if err = tx.QueryRow(r.Context(), `SELECT id FROM op_fixture_requests WHERE tenant_id=$1 AND id=$2 AND browser_digest=$3 AND csrf_digest=$4 AND approved_at IS NULL AND code_digest IS NULL AND expires_at>clock_timestamp() FOR UPDATE`, f.store.Tenant, id, browserHash, digest(csrf)).Scan(&locked); err != nil {
		protocolError(w, 403, "access_denied")
		return
	}
	sessionToken, err := tokenRandom()
	if err != nil {
		protocolError(w, 500, "server_error")
		return
	}
	session := uuid.NewString()
	var authTime time.Time
	err = tx.QueryRow(r.Context(), `WITH moment AS(SELECT clock_timestamp() AS now) INSERT INTO op_fixture_sessions(tenant_id,id,user_id,auth_epoch,token_digest,created_at,expires_at) SELECT $1,$2,$3,$4,$5,now,now+$6::bigint*interval '1 microsecond' FROM moment RETURNING created_at`, f.store.Tenant, session, f.loginUser, epoch, digest(sessionToken), f.store.Policy.SessionLifetime.Microseconds()).Scan(&authTime)
	if err != nil {
		protocolError(w, 503, "server_error")
		return
	}
	result, err := tx.Exec(r.Context(), `UPDATE op_fixture_requests SET csrf_digest=NULL,user_id=$3,session_id=$4,auth_epoch=$5,subject=$6,membership_authority=$7,approved_at=$8 WHERE tenant_id=$1 AND id=$2 AND approved_at IS NULL`, f.store.Tenant, id, f.loginUser, session, epoch, subject, membership, authTime)
	if err != nil || result.RowsAffected() != 1 {
		protocolError(w, 503, "server_error")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		protocolError(w, 503, "server_error")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: sessionToken, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(f.store.Policy.SessionLifetime.Seconds())})
	http.Redirect(w, r, f.store.Origin+"/authorize/callback?id="+url.QueryEscape(id), 303)
}
