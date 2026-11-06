package sso

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

// Outcomes of a connector check, as the contract names them.
const (
	CheckPassed                = "passed"
	CheckNotConfigured         = "not_configured"
	CheckUnreachable           = "unreachable"
	CheckCertificateUntrusted  = "certificate_untrusted"
	CheckCredentialsRejected   = "credentials_rejected"
	CheckCallbackNotRegistered = "callback_not_registered"
	CheckUnexpectedAnswer      = "unexpected_answer"
)

// Gitea's token endpoint checks the client ID, the client secret and the redirect URI, in that
// order, before it looks at the code, and names each refusal (routers/web/auth/oauth2_provider.go,
// handleAuthorizationCode, v28.3.0). A check sends a code that cannot exist, so the code's refusal
// means everything before it was accepted (docs/research/sso-page.md).
const (
	giteaUnknownClient   = "invalid_client"
	giteaRefusedClient   = "unauthorized_client"
	giteaWrongSecret     = "invalid client secret"
	giteaUnregisteredURI = "unexpected redirect URI"
	giteaUnknownCode     = "client is not authorized"
)

var providerErrorCode = regexp.MustCompile(`^[a-z_]{1,64}$`)

// providerCheck is what one token request said about a connector.
type providerCheck struct {
	outcome string
	// providerError is the provider's own error code for an unexpected answer, when it is a
	// plain OAuth error code; never its description, which may echo input.
	providerError string
}

// checkClient asks the token endpoint to exchange a code that cannot exist, with the client's
// credentials and one callback. Nothing is created at the provider. It uses the transport sign-in
// uses and shares its exchange slots, so checks cannot crowd out sign-ins.
func (p *giteaProvider) checkClient(ctx context.Context, clientID, secret, callback string) (providerCheck, error) {
	if !canonicalID(clientID) || !p.callbacks[callback] || !safeOpaque(secret, maximumSecretBytes) {
		return providerCheck{}, errors.New("SSO_AUTHORIZATION_INPUT_INVALID")
	}
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	default:
		return providerCheck{}, errors.New("SSO_CAPACITY_EXCEEDED")
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(p.policy.OperationSeconds))
	defer cancel()
	form := url.Values{"grant_type": {"authorization_code"}, "redirect_uri": {callback}, "code": {security.Token()}, "code_verifier": {security.Token()}}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, p.configuration.Origin+GiteaTokenPath, strings.NewReader(form.Encode()))
	if e != nil {
		return providerCheck{outcome: CheckUnreachable}, nil
	}
	req.GetBody = nil
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(clientID, secret)
	response, e := p.client.Do(req)
	if e != nil {
		var certificate *tls.CertificateVerificationError
		if errors.As(e, &certificate) {
			return providerCheck{outcome: CheckCertificateUntrusted}, nil
		}
		return providerCheck{outcome: CheckUnreachable}, nil
	}
	defer response.Body.Close()
	return p.classify(response), nil
}

// classify reads a token endpoint's refusal. Only Gitea's exact refusal of the code passes, so a
// provider that words its answers differently reads as unexpected, never as a pass.
func (p *giteaProvider) classify(response *http.Response) providerCheck {
	unexpected := providerCheck{outcome: CheckUnexpectedAnswer}
	if response.StatusCode != http.StatusBadRequest && response.StatusCode != http.StatusUnauthorized {
		return unexpected
	}
	media, _, e := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if e != nil || media != "application/json" || len(response.Header.Values("Content-Type")) != 1 {
		return unexpected
	}
	raw, e := io.ReadAll(io.LimitReader(response.Body, int64(p.policy.ResponseBytes)+1))
	if e != nil || len(raw) > p.policy.ResponseBytes {
		return unexpected
	}
	fields, e := objectFields(raw, maximumProviderFields)
	if e != nil {
		return unexpected
	}
	var code, description string
	if json.Unmarshal(fields["error"], &code) != nil {
		return unexpected
	}
	if _, present := fields["error_description"]; present && json.Unmarshal(fields["error_description"], &description) != nil {
		return unexpected
	}
	switch {
	case code == giteaUnknownClient:
		return providerCheck{outcome: CheckCredentialsRejected}
	case code == giteaRefusedClient && description == giteaWrongSecret:
		return providerCheck{outcome: CheckCredentialsRejected}
	case code == giteaRefusedClient && description == giteaUnregisteredURI:
		return providerCheck{outcome: CheckCallbackNotRegistered}
	case code == giteaRefusedClient && description == giteaUnknownCode:
		return providerCheck{outcome: CheckPassed}
	}
	if providerErrorCode.MatchString(code) {
		unexpected.providerError = code
	}
	return unexpected
}
