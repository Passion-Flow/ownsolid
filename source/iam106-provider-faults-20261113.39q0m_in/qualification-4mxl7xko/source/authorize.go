package boundary

import (
	"errors"
	"net/http"

	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

func storageFailure(err error) bool {
	var failure *oidc.Error
	return errors.As(err, &failure) && failure.ErrorType == oidc.ServerError
}
func (f *fixtureProvider) authorizationError(w http.ResponseWriter, r *http.Request, c registeredClient, state string, err error) {
	refusal := oidc.ErrInvalidRequest()
	if storageFailure(err) {
		refusal = oidc.ErrServerError()
	}
	refusal.State = state
	destination, e := op.AuthResponseURL(c.Redirect, oidc.ResponseTypeCode, oidc.ResponseModeQuery, refusal, f.sdk.Encoder())
	if e != nil {
		protocolError(w, 500, "server_error")
		return
	}
	http.Redirect(w, r, destination, 302)
}
func (f *fixtureProvider) authorize(w http.ResponseWriter, r *http.Request, c registeredClient) {
	input, err := op.ParseAuthorizeRequest(r, f.sdk.Decoder())
	if err != nil {
		protocolError(w, 400, "invalid_request")
		return
	}
	if _, err = op.ValidateAuthRequestClient(r.Context(), input, c, nil); err != nil {
		f.authorizationError(w, r, c, input.State, err)
		return
	}
	request, err := f.store.CreateAuthRequest(r.Context(), input, "")
	if err != nil {
		f.authorizationError(w, r, c, input.State, err)
		return
	}
	op.RedirectToLogin(request.GetID(), c, w, r)
}
func (f *fixtureProvider) callback(w http.ResponseWriter, r *http.Request) {
	request, err := f.store.AuthRequestByID(r.Context(), r.URL.Query().Get("id"))
	if err != nil {
		status := 400
		if storageFailure(err) {
			status = 503
		}
		protocolError(w, status, "invalid_request")
		return
	}
	client, _, err := f.store.registered(r.Context(), f.store.pool, request.GetClientID(), "")
	if err != nil {
		protocolError(w, 400, "invalid_request")
		return
	}
	if !request.Done() {
		op.RedirectToLogin(request.GetID(), client, w, r)
		return
	}
	response, err := op.BuildAuthResponseCodeResponsePayload(r.Context(), request, f.sdk)
	if err != nil {
		f.authorizationError(w, r, client, request.GetState(), err)
		return
	}
	destination, err := op.AuthResponseURL(request.GetRedirectURI(), oidc.ResponseTypeCode, oidc.ResponseModeQuery, response, f.sdk.Encoder())
	if err != nil {
		protocolError(w, 500, "server_error")
		return
	}
	http.Redirect(w, r, destination, 302)
}
