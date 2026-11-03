package scope

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestCumulativeConfigurationRetainsEveryUnrelatedPrivateValue(t *testing.T) {
	original := []byte(`{"server":{"application_profile":"localization","transport_mode":"direct_https","listen_address":"0.0.0.0:8443","admin_origin":"https://admin.example.invalid:8443","tls_private_key_file":"/run/projectone/private/key"},"database":{"dsn_file":"/run/projectone/private/runtime-dsn"},"security":{"auth_hmac_key_file":"/run/projectone/private/auth-key"},"sso":{"enabled":false,"keyring_file":"/run/projectone/private/sso-key"},"feature":{"retry":3,"default":false}}`)
	target, e := cumulativeProfile(original, "/run/projectone/private/admin-view.json")
	if e != nil {
		t.Fatal(e)
	}
	var before, after map[string]any
	json.Unmarshal(original, &before)
	json.Unmarshal(target, &after)
	if after["release_view_config_file"] != "/run/projectone/private/admin-view.json" || after["server"].(map[string]any)["application_profile"] != "releases" {
		t.Fatal("explicit transition missing")
	}
	delete(after, "release_view_config_file")
	after["server"].(map[string]any)["application_profile"] = "localization"
	if !reflect.DeepEqual(before, after) {
		t.Fatal("private authority, transport or false policy changed")
	}
	for _, bad := range []string{strings.Replace(string(original), "localization", "platform", 1), strings.Replace(string(original), "application_profile", "Application_profile", 1), strings.Replace(string(original), `"feature":`, `"release_view_config_file":"/foreign","feature":`, 1), strings.Replace(string(original), `"application_profile":"localization"`, `"application_profile":"localization","application_profile":"localization"`, 1), `{"server":null}`} {
		if _, e := cumulativeProfile([]byte(bad), "/run/projectone/private/admin-view.json"); e == nil {
			t.Fatal("foreign or ambiguous source profile admitted")
		}
	}
}
