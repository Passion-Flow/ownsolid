package boundary

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

var errRequest = errors.New("OP_REQUEST_REFUSED")

type requestPolicy struct{ MaxBody, MaxCode, MaxRedirect, MaxClient, MaxSecret int }
type tokenInput struct {
	Client, Secret string
	Form           url.Values
}

func header(r *http.Request, name string) (string, bool) {
	var values []string
	for k, v := range r.Header {
		if strings.EqualFold(k, name) {
			values = append(values, v...)
		}
	}
	returnValue := ""
	if len(values) == 1 {
		returnValue = values[0]
	}
	return returnValue, len(values) == 1
}
func printable(s string, minimum, maximum int, space bool) bool {
	if len(s) < minimum || len(s) > maximum {
		return false
	}
	for _, r := range s {
		if r < 0x21 || r > 0x7e {
			if !(space && r == 0x20) {
				return false
			}
		}
	}
	return true
}
func verifier(s string) bool {
	if len(s) < 43 || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-._~", r)) {
			return false
		}
	}
	return true
}

func (p requestPolicy) valid() bool {
	return p.MaxBody >= 256 && p.MaxBody <= 32768 && p.MaxCode >= 43 && p.MaxCode <= 4096 && p.MaxRedirect >= 64 && p.MaxRedirect <= 4096 && p.MaxClient >= 1 && p.MaxClient <= 256 && p.MaxSecret >= 1 && p.MaxSecret <= 4096
}
func parseBasic(r *http.Request, p requestPolicy) (string, string, error) {
	if r == nil || !p.valid() {
		return "", "", errConfig
	}
	auth, ok := header(r, "Authorization")
	if !ok || len(auth) > 8192 || len(auth) < 7 || !strings.EqualFold(auth[:6], "Basic ") {
		return "", "", errRequest
	}
	raw, e := base64.StdEncoding.Strict().DecodeString(auth[6:])
	if e != nil || base64.StdEncoding.EncodeToString(raw) != auth[6:] {
		return "", "", errRequest
	}
	id, secret, ok := strings.Cut(string(raw), ":")
	if !ok {
		return "", "", errRequest
	}
	id, e = url.QueryUnescape(id)
	if e != nil {
		return "", "", errRequest
	}
	secret, e = url.QueryUnescape(secret)
	if e != nil || !printable(id, 1, p.MaxClient, true) || !printable(secret, 1, p.MaxSecret, true) {
		return "", "", errRequest
	}
	return id, secret, nil
}
func boundedForm(r *http.Request, maximum int) (url.Values, error) {
	if r == nil || r.Context().Err() != nil || r.Method != "POST" || r.URL == nil || r.URL.RawQuery != "" || r.URL.ForceQuery || r.Body == nil || r.ContentLength > int64(maximum) {
		return nil, errRequest
	}
	ct, ok := header(r, "Content-Type")
	if !ok {
		return nil, errRequest
	}
	media, params, e := mime.ParseMediaType(ct)
	if e != nil || media != "application/x-www-form-urlencoded" || len(params) > 1 || len(params) == 1 && !strings.EqualFold(params["charset"], "utf-8") {
		return nil, errRequest
	}
	body, e := io.ReadAll(io.LimitReader(r.Body, int64(maximum)+1))
	if e != nil || len(body) > maximum {
		return nil, errRequest
	}
	fields, e := url.ParseQuery(string(body))
	if e != nil || len(fields) > 8 {
		return nil, errRequest
	}
	for k, v := range fields {
		if !printable(k, 1, 64, false) || len(v) != 1 {
			return nil, errRequest
		}
	}
	if r.Context().Err() != nil {
		return nil, r.Context().Err()
	}
	r.Form, r.PostForm = cloneForm(fields), cloneForm(fields)
	r.Body.Close()
	r.Body = io.NopCloser(strings.NewReader(string(body)))
	r.ContentLength = int64(len(body))
	return fields, nil
}
func parseToken(r *http.Request, p requestPolicy) (tokenInput, error) {
	if r == nil || !p.valid() {
		return tokenInput{}, errConfig
	}
	if r.Context().Err() != nil {
		return tokenInput{}, r.Context().Err()
	}
	id, secret, e := parseBasic(r, p)
	if e != nil {
		return tokenInput{}, e
	}
	fields, e := boundedForm(r, p.MaxBody)
	if e != nil || len(fields) != 4 {
		return tokenInput{}, errRequest
	}
	for k := range fields {
		switch k {
		case "grant_type", "code", "redirect_uri", "code_verifier":
		default:
			return tokenInput{}, errRequest
		}
	}
	if fields.Get("grant_type") != "authorization_code" || !printable(fields.Get("code"), 1, p.MaxCode, false) || !printable(fields.Get("redirect_uri"), 1, p.MaxRedirect, false) || !verifier(fields.Get("code_verifier")) {
		return tokenInput{}, errRequest
	}
	u, e := url.Parse(fields.Get("redirect_uri"))
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return tokenInput{}, errRequest
	}
	return tokenInput{Client: id, Secret: secret, Form: fields}, nil
}
func parseRevocation(r *http.Request, p requestPolicy, c *providerCrypto) (tokenInput, error) {
	if r == nil || !p.valid() {
		return tokenInput{}, errConfig
	}
	id, secret, e := parseBasic(r, p)
	if e != nil {
		return tokenInput{}, e
	}
	fields, e := boundedForm(r, p.MaxBody)
	if e != nil || len(fields) < 1 || len(fields) > 2 {
		return tokenInput{}, errRequest
	}
	for k := range fields {
		switch k {
		case "token", "token_type_hint":
		default:
			return tokenInput{}, errRequest
		}
	}
	if fields.Has("token_type_hint") && fields.Get("token_type_hint") != "access_token" {
		return tokenInput{}, errRequest
	}
	if _, e = c.Decrypt(fields.Get("token")); e != nil {
		return tokenInput{}, errRequest
	}
	return tokenInput{Client: id, Secret: secret, Form: fields}, nil
}

func cloneForm(v url.Values) url.Values {
	out := url.Values{}
	for key, values := range v {
		out[key] = append([]string(nil), values...)
	}
	return out
}
func tokenGate(r *http.Request, p requestPolicy, authenticate func(context.Context, string, string) (string, error), next func(*http.Request, string) error) error {
	input, err := parseToken(r, p)
	if err != nil {
		return err
	}
	if authenticate == nil || next == nil {
		return errConfig
	}
	resolved, err := authenticate(r.Context(), input.Client, input.Secret)
	if err != nil || resolved != input.Client {
		return errRequest
	}
	if r.Context().Err() != nil {
		return r.Context().Err()
	}
	return next(r, resolved)
}

func userInfoToken(r *http.Request, c *strictCrypto) (string, error) {
	if r != nil && r.Context().Err() != nil {
		return "", r.Context().Err()
	}
	if r == nil || r.URL == nil || len(r.TransferEncoding) != 0 || r.Method != "GET" || r.URL.RawQuery != "" || r.URL.ForceQuery || r.ContentLength != 0 {
		return "", errRequest
	}
	auth, ok := header(r, "Authorization")
	if !ok || len(auth) < 8 || !strings.EqualFold(auth[:7], "Bearer ") {
		return "", errRequest
	}
	token := auth[7:]
	if _, e := c.Decrypt(token); e != nil {
		return "", errToken
	}
	if r.Context().Err() != nil {
		return "", r.Context().Err()
	}
	return token, nil
}
