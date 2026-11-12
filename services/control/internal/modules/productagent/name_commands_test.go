package productagent

import (
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/applications"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
)

func TestQuotedApplicationNameCommands(t *testing.T) {
	s, err := New(&applications.Service{Identity: &identity.Service{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ input, name string }{
		{`find application "Sales  East"`, "Sales  East"},
		{`FIND   APPLICATION "Sales East"`, "Sales East"},
		{"\u67e5\u627e\u5e94\u7528 \"\u9500\u552e\u52a9\u624b\"", "\u9500\u552e\u52a9\u624b"},
		{`find application " Cafe\u0301 "`, "Caf\u00e9"},
		{`find application "open home; disable platform"`, "open home; disable platform"},
		{`find application "100%_\"quoted\""`, "100%_\"quoted\""},
		{`find application "` + strings.Repeat("\u5b57", 120) + `"`, strings.Repeat("\u5b57", 120)},
		{`find application "` + strings.Repeat(`\ud83d\ude00`, 120) + `"`, strings.Repeat("\U0001f600", 120)},
	} {
		cmd, err := s.resolveCommand(tc.input)
		if err != nil || cmd.id != findApplication || cmd.applicationName != tc.name {
			t.Fatalf("quoted name resolution: %+v, %v", cmd, err)
		}
		changed := cmd
		changed.applicationName += "x"
		if cmd.fingerprint("") == changed.fingerprint("") {
			t.Fatal("execution digest omitted the name")
		}
	}
	for _, input := range []string{
		`find application "x"; open home`, `find application "x" "y"`, `find application {"name":"x"}`,
		`find application ""`, `find application "  "`, `find application "x\ny"`, `find application "x\u0000y"`,
		`find application "` + strings.Repeat("x", 121) + `"`, "find\napplication \"x\"", "find application \"\xff\"",
		`find application "unterminated`, `do not find application "x"`,
	} {
		if _, err := s.resolveCommand(input); err == nil {
			t.Fatalf("admitted malformed name expression %q", input)
		}
	}
	cmd, _ := s.resolveCommand(`find application "x"`)
	actor := identity.Auth{Me: identity.Me{Audience: "portal", Workspace: &identity.Workspace{}}}
	if _, err := s.admit(cmd, actor, "cursor"); err != ErrInvalidCommand {
		t.Fatal("name lookup accepted a list cursor", err)
	}
	actor.Me.Audience = "admin"
	if _, err := s.admit(cmd, actor, ""); err != ErrCommandDenied {
		t.Fatal("admin invoked Portal name lookup", err)
	}
	delete(s.tools, listApplications)
	actor.Me.Audience = "portal"
	if _, err := s.admit(cmd, actor, ""); err != ErrCommandDenied {
		t.Fatal("lookup bypassed the reviewed read", err)
	}
}
