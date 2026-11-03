package main

import (
	"bytes"
	"encoding/json"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
	"strings"
	"testing"
)

func TestServingCheckRequiresExactExplicitApplicationBeforeServing(t *testing.T) {
	path := probeCLIConfiguration(t, "127.0.0.1:1")
	base := servingCLIArguments(t, path)
	for _, value := range []string{"", "platform", "releases", "MVP-postgresql-cpu-v1"} {
		var output bytes.Buffer
		err := runServingCheckCommand(append(append([]string{}, base...), "--expected-application-profile", value), &output)
		if err == nil || err.Error() != "SERVING_TRANSPORT_ARGUMENT_INVALID" || output.Len() != 0 {
			t.Fatalf("unowned/empty application selector accepted: %v", err)
		}
	}
	var output bytes.Buffer
	err := runServingCheckCommand(append(append([]string{}, base...), "--expected-application-profile", "mvp-postgresql-cpu-v1"), &output)
	if err == nil || err.Error() != "SERVING_APPLICATION_PROFILE_MISMATCH" || output.Len() != 0 {
		t.Fatalf("private legacy configuration satisfied final graph expectation: %v", err)
	}
}

func TestServingCheckRequiresExactClosedReaderPolicyBeforeTransportSuccess(t *testing.T) {
	path := probeCLIConfiguration(t, "127.0.0.1:1")
	base := servingCLIArguments(t, path)
	policy := view.Configuration{Directory: "/run/projectone-release-view", ScopeID: strings.Repeat("a", 32), ManifestSHA256: strings.Repeat("b", 64), ConfigurationSHA256: strings.Repeat("c", 64), PublisherUID: 0, PageSize: 1, OperationSeconds: 1}
	raw, _ := json.Marshal(policy)
	for _, input := range []struct{ raw, code string }{{string(raw), "SERVING_RELEASE_VIEW_POLICY_MISMATCH"}, {`{"publisher_uid":null}`, "SERVING_TRANSPORT_ARGUMENT_INVALID"}} {
		var output bytes.Buffer
		err := runServingCheckCommand(append(append([]string{}, base...), "--expected-release-view-json", input.raw), &output)
		if err == nil || err.Error() != input.code || output.Len() != 0 {
			t.Fatalf("unbound reader policy accepted: %v", err)
		}
	}
}
