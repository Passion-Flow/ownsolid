package http_test

import (
	"encoding/json"
	"io"
	"net/http"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"strings"
	"testing"
)

func TestPublicConfigurationIsAllowlistedAndConfiguredRateApplies(t *testing.T) {
	s := fixture.New(t)
	policy := config.DefaultRuntimePolicy()
	policy.UI.PollMilliseconds = 7000
	policy.Rates.CSRFCount = 1
	s.Config.Runtime = &policy
	s.Config.Database.DSNFile = "synthetic-private-dsn-location"
	s.Config.Security.AuthHMACKeyFile = "synthetic-private-key-location"
	server := accountTLSServer(t, transport.New(s))
	portal := newClient(t, server, "portal", nil)
	get := func(path string) *http.Response {
		r, _ := http.NewRequest("GET", server.URL+path, nil)
		r.Host = "portal.example.invalid"
		response, err := portal.http.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	response := get("/runtime-config.json")
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("public configuration unavailable")
	}
	for _, secret := range []string{"synthetic-private", "database", "security", "smtp", "runtime"} {
		if strings.Contains(string(body), secret) {
			t.Fatal("private config exposed")
		}
	}
	var envelope struct {
		Data struct {
			UI config.UIPolicy `json:"ui"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Data.UI.PollMilliseconds != 7000 {
		t.Fatal("nondefault public setting not returned", err)
	}
	for index, want := range []int{200, 429} {
		response = get("/api/v1/auth/csrf")
		response.Body.Close()
		if response.StatusCode != want {
			t.Fatal("configured CSRF rate not enforced", index, response.StatusCode)
		}
	}
}
