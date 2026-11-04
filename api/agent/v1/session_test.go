package v1

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestAgentMessageValidation(t *testing.T) {
	cases := []struct {
		name     string
		message  AgentMessage
		previous uint64
		valid    bool
	}{
		{"first heartbeat", AgentMessage{ID: 1, Heartbeat: &Handshake{}}, 0, true},
		{"next claim", AgentMessage{ID: 2, Claim: &ClaimRequest{WaitSeconds: MaxClaimWaitSeconds}}, 1, true},
		{"skipped id", AgentMessage{ID: 3, Heartbeat: &Handshake{}}, 1, false},
		{"repeated id", AgentMessage{ID: 1, Heartbeat: &Handshake{}}, 1, false},
		{"empty", AgentMessage{ID: 1}, 0, false},
		{"two exchanges", AgentMessage{ID: 1, Heartbeat: &Handshake{}, Claim: &ClaimRequest{}}, 0, false},
		{"negative wait", AgentMessage{ID: 1, Claim: &ClaimRequest{WaitSeconds: -1}}, 0, false},
		{"wait above interval", AgentMessage{ID: 1, Claim: &ClaimRequest{WaitSeconds: MaxClaimWaitSeconds + 1}}, 0, false},
		{"claim with cursor", AgentMessage{ID: 2, Claim: &ClaimRequest{AcknowledgedSequence: 7}}, 1, true},
		{"cursor above the JSON range", AgentMessage{ID: 2, Claim: &ClaimRequest{AcknowledgedSequence: MaxTaskSequence + 1}}, 1, false},
		{"ack", AgentMessage{ID: 3, Ack: &TaskAcknowledgement{TaskID: "0b5d7a52-6a5e-4d40-9b7e-0d9a3c1f2e44", Sequence: 4, LeaseToken: strings.Repeat("a", 43)}}, 2, true},
		{"ack without token", AgentMessage{ID: 3, Ack: &TaskAcknowledgement{TaskID: "0b5d7a52-6a5e-4d40-9b7e-0d9a3c1f2e44", Sequence: 4}}, 2, false},
		{"ack with bad task", AgentMessage{ID: 3, Ack: &TaskAcknowledgement{TaskID: "task", Sequence: 4, LeaseToken: strings.Repeat("a", 43)}}, 2, false},
		{"ack and claim", AgentMessage{ID: 3, Claim: &ClaimRequest{}, Ack: &TaskAcknowledgement{TaskID: "0b5d7a52-6a5e-4d40-9b7e-0d9a3c1f2e44", Sequence: 4, LeaseToken: strings.Repeat("a", 43)}}, 2, false},
	}
	for _, c := range cases {
		if e := c.message.Validate(c.previous); (e == nil) != c.valid {
			t.Errorf("%s: %v", c.name, e)
		}
	}
}

func TestControlMessageAnswersItsRequest(t *testing.T) {
	heartbeat := AgentMessage{ID: 4, Heartbeat: &Handshake{}}
	claim := AgentMessage{ID: 5, Claim: &ClaimRequest{}}
	ack := AgentMessage{ID: 6, Ack: &TaskAcknowledgement{}}
	cases := []struct {
		name    string
		reply   ControlMessage
		request AgentMessage
		valid   bool
	}{
		{"disposition", ControlMessage{ReplyTo: 4, Disposition: &Disposition{}}, heartbeat, true},
		{"claim", ControlMessage{ReplyTo: 5, Claim: &Claim{}}, claim, true},
		{"problem", ControlMessage{ReplyTo: 5, Problem: &SessionProblem{Code: "AGENT_UNAUTHORIZED", Status: 401}}, claim, true},
		{"wrong reply", ControlMessage{ReplyTo: 3, Disposition: &Disposition{}}, heartbeat, false},
		{"claim for heartbeat", ControlMessage{ReplyTo: 4, Claim: &Claim{}}, heartbeat, false},
		{"disposition for claim", ControlMessage{ReplyTo: 5, Disposition: &Disposition{}}, claim, false},
		{"two replies", ControlMessage{ReplyTo: 4, Disposition: &Disposition{}, Problem: &SessionProblem{Code: "X", Status: 500}}, heartbeat, false},
		{"empty", ControlMessage{ReplyTo: 4}, heartbeat, false},
		{"success status", ControlMessage{ReplyTo: 4, Problem: &SessionProblem{Code: "X", Status: 200}}, heartbeat, false},
		{"empty code", ControlMessage{ReplyTo: 4, Problem: &SessionProblem{Status: 503}}, heartbeat, false},
		{"acknowledged", ControlMessage{ReplyTo: 6, Acknowledged: &RenewedLease{}}, ack, true},
		{"claim for ack", ControlMessage{ReplyTo: 6, Claim: &Claim{}}, ack, false},
		{"acknowledged for claim", ControlMessage{ReplyTo: 5, Acknowledged: &RenewedLease{}}, claim, false},
		{"resync", ControlMessage{ReplyTo: 5, Claim: &Claim{Resync: &Resync{AcknowledgedSequence: 3}}}, claim, true},
		{"lease and resync", ControlMessage{ReplyTo: 5, Claim: &Claim{Lease: &Lease{}, Resync: &Resync{}}}, claim, false},
	}
	for _, c := range cases {
		if e := c.reply.Validate(c.request); (e == nil) != c.valid {
			t.Errorf("%s: %v", c.name, e)
		}
	}
}

func TestSessionCodecIsStrict(t *testing.T) {
	codec := SessionCodec{}
	if codec.Name() != "json" {
		t.Fatal("content subtype")
	}
	b, e := codec.Marshal(&AgentMessage{ID: 1, Claim: &ClaimRequest{WaitSeconds: 2}})
	if e != nil || string(b) != `{"id":1,"claim":{"wait_seconds":2}}` {
		t.Fatalf("encoded %s %v", b, e)
	}
	var m AgentMessage
	if e = codec.Unmarshal(b, &m); e != nil || m.ID != 1 || m.Claim.WaitSeconds != 2 {
		t.Fatalf("decoded %+v %v", m, e)
	}
	for _, bad := range []string{`{"id":1,"unknown":true}`, `{"id":1} {}`, `[`, `{"id":"1"}`} {
		if codec.Unmarshal([]byte(bad), &m) == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	large := `{"id":1,"heartbeat":{"protocol_version":"` + strings.Repeat("a", MaxSessionMessageBytes) + `"}}`
	if codec.Unmarshal([]byte(large), &m) == nil {
		t.Error("accepted an oversized message")
	}
	if _, e = codec.Marshal(&AgentMessage{ID: 1, Heartbeat: &Handshake{ProtocolVersion: strings.Repeat("a", MaxSessionMessageBytes)}}); e == nil {
		t.Error("encoded an oversized message")
	}
}

func TestSessionPathIsTheGRPCMethod(t *testing.T) {
	if SessionPath != "/ownsolid.agent.v1.AgentSession/Connect" {
		t.Fatal(SessionPath)
	}
	if MaxClaimWaitSeconds != HeartbeatSeconds || SessionIdleSeconds <= MaxClaimWaitSeconds {
		t.Fatal("a held claim must end before the session counts as idle")
	}
}

// TestSessionSchemaMatchesTypes keeps schema.json and the Go session types in step.
func TestSessionSchemaMatchesTypes(t *testing.T) {
	raw, e := os.ReadFile("schema.json")
	if e != nil {
		t.Fatal(e)
	}
	var schema struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if e = json.Unmarshal(raw, &schema); e != nil {
		t.Fatal(e)
	}
	for name, value := range map[string]any{"AgentMessage": AgentMessage{}, "ControlMessage": ControlMessage{}, "ClaimRequest": ClaimRequest{}, "SessionProblem": SessionProblem{}, "Claim": Claim{}, "TaskAcknowledgement": TaskAcknowledgement{}, "TaskAcknowledgementRequest": TaskAcknowledgementRequest{}, "Resync": Resync{}} {
		declared, ok := schema.Components.Schemas[name]
		if !ok {
			t.Fatalf("schema.json has no %s", name)
		}
		fields := map[string]bool{}
		kind := reflect.TypeOf(value)
		for i := 0; i < kind.NumField(); i++ {
			tag, _, _ := strings.Cut(kind.Field(i).Tag.Get("json"), ",")
			fields[tag] = true
			if _, ok := declared.Properties[tag]; !ok {
				t.Errorf("%s.%s is not in schema.json", name, tag)
			}
		}
		for property := range declared.Properties {
			if !fields[property] {
				t.Errorf("schema.json %s.%s has no Go field", name, property)
			}
		}
	}
}
