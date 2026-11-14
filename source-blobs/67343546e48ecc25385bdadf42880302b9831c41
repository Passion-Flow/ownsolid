package v1

import "testing"

func TestEveryCallHasAPriority(t *testing.T) {
	seen := map[Call]bool{}
	for _, c := range Calls() {
		if !c.Known() || seen[c] {
			t.Fatalf("%q is unknown or listed twice", c)
		}
		seen[c] = true
		if p := c.Priority(); p.String() == "unknown" {
			t.Errorf("%q has no named priority", c)
		}
	}
	if len(seen) != len(callPriorities) {
		t.Fatalf("Calls lists %d of %d calls", len(seen), len(callPriorities))
	}
	if Call("telemetry").Known() || Call("").Known() {
		t.Fatal("an unlisted call is known")
	}
}

func TestEveryCallDeclaresItsCost(t *testing.T) {
	if len(callCosts) != len(Calls()) {
		t.Fatalf("%d costs for %d calls", len(callCosts), len(Calls()))
	}
	for _, c := range Calls() {
		cost := c.Cost()
		if cost.Statements < 1 {
			t.Errorf("%s declares no statements", c)
		}
		switch cost.Growth {
		case GrowthFixed, GrowthBatch, GrowthBytes:
		default:
			t.Errorf("%s declares growth %q", c, cost.Growth)
		}
	}
	if (Call("telemetry").Cost() != Cost{}) {
		t.Fatal("an unlisted call declares a cost")
	}
}

func TestPriorityOrderShedsBulkFirst(t *testing.T) {
	order := Priorities()
	for i, p := range order {
		if int(p) != i {
			t.Fatalf("Priorities is not ordered highest first: %v", order)
		}
	}
	for _, c := range []struct {
		call Call
		want Priority
	}{
		{CallResult, PriorityQuarantine},
		{CallStop, PriorityQuarantine},
		{CallHeartbeat, PriorityHeartbeat},
		{CallClaim, PriorityHeartbeat},
		{CallAcknowledge, PriorityHeartbeat},
		{CallRenew, PriorityHeartbeat},
		{CallEvents, PriorityTelemetry},
		{CallEnroll, PriorityBulk},
		{CallDownload, PriorityBulk},
		{CallUpload, PriorityBulk},
	} {
		if got := c.call.Priority(); got != c.want {
			t.Errorf("%s: priority %s, want %s", c.call, got, c.want)
		}
	}
	if Priority(-1).String() != "unknown" || Priority(len(priorityNames)).String() != "unknown" {
		t.Fatal("an out-of-range priority has a name")
	}
}

func TestOnlyClaimsWaitForWork(t *testing.T) {
	for _, c := range Calls() {
		if c.WaitsForWork() != (c == CallClaim) {
			t.Errorf("%s: WaitsForWork %v", c, c.WaitsForWork())
		}
	}
}

func TestPathCallClassifiesEveryAgentPath(t *testing.T) {
	const node, task = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	base := "/agent/v1/nodes/" + node
	for path, want := range map[string]Call{
		"/agent/v1/enroll":     CallEnroll,
		base + "/heartbeat":    CallHeartbeat,
		base + "/tasks/claims": CallClaim,
		base + "/tasks/" + task + "/" + TaskAcknowledgementsPath:                   CallAcknowledge,
		base + "/tasks/" + task + "/renewals":                                      CallRenew,
		base + "/tasks/" + task + "/results":                                       CallResult,
		base + "/tasks/" + task + "/training-stops":                                CallStop,
		base + "/tasks/" + task + "/model-preparation-stops":                       CallStop,
		base + "/tasks/" + task + "/training-events":                               CallEvents,
		base + "/tasks/" + task + "/model-preparation-events":                      CallEvents,
		base + "/tasks/" + task + "/artifact":                                      CallDownload,
		base + "/tasks/" + task + "/model-manifest":                                CallDownload,
		base + "/tasks/" + task + "/model-files":                                   CallDownload,
		base + "/tasks/" + task + "/training-input-files":                          CallDownload,
		base + "/tasks/" + task + "/model-preparation-input-files":                 CallDownload,
		base + "/tasks/" + task + "/image-registry/manifest":                       CallDownload,
		base + "/tasks/" + task + "/image-registry/ranges":                         CallDownload,
		base + "/tasks/" + task + "/training-uploads":                              CallUpload,
		base + "/tasks/" + task + "/training-uploads/" + task + "/chunks":          CallUpload,
		base + "/tasks/" + task + "/model-preparation-uploads/" + task + "/finish": CallUpload,
	} {
		call, named, ok := PathCall(path)
		if !ok || call != want {
			t.Errorf("%s: %q %v, want %q", path, call, ok, want)
		}
		if want == CallEnroll && named != "" || want != CallEnroll && named != node {
			t.Errorf("%s: node %q", path, named)
		}
	}
	for _, path := range []string{
		"", "/agent/v1/", "/agent/v1/enroll/x", "/api/v1/nodes/" + node + "/heartbeat", "/agent/v1/nodes//heartbeat",
		base, base + "/heartbeat/x", base + "/tasks", base + "/tasks/claims/x", base + "/tasks/" + task,
		base + "/tasks/" + task + "/unknown", base + "/tasks/" + task + "/results/x", base + "/tasks//results",
	} {
		if call, _, ok := PathCall(path); ok {
			t.Errorf("%s classified as %q", path, call)
		}
	}
}
