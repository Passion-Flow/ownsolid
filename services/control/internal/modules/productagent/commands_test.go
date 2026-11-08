package productagent

import (
	"errors"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/applications"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
)

func TestLocalizedCommandDestinations(t *testing.T) {
	catalog, err := loadCommands(locales)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		input, audience, command, path string
	}{
		{"  OPEN   NODES  ", "admin", "open_nodes", "/nodes"},
		{"\u6253\u5f00\u8282\u70b9", "admin", "open_nodes", "/nodes"},
		{"open nodes", "portal", "open_nodes", ""},
		{"open keys", "admin", "open_keys", ""},
		{"\u6253\u5f00\u5bc6\u94a5", "portal", "open_keys", "/keys"},
		{"open\u3000models", "portal", "open_models", "/models"},
		{"\u67e5\u770b\u5e94\u7528", "portal", listApplications, ""},
		{"show conversations", "portal", listConversations, ""},
	} {
		t.Run(test.command+"/"+test.audience, func(t *testing.T) {
			cmd := catalog.aliases[normalize(test.input)]
			if cmd.id != test.command || cmd.destination(test.audience) != test.path {
				t.Fatalf("expected %s at %q, got %#v", test.command, test.path, cmd)
			}
		})
	}
}

func TestCommandRejection(t *testing.T) {
	service, err := New(&applications.Service{Identity: &identity.Service{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		input, audience, cursor string
		err                     error
	}{
		{"open home", "", "", ErrCommandDenied},
		{"open home", "gateway", "", ErrCommandDenied},
		{"open nodes", "portal", "", ErrCommandDenied},
		{"open api keys", "admin", "", ErrCommandDenied},
		{"show applications", "admin", "", ErrCommandDenied},
		{"show applications", "portal", "", ErrWorkspaceRequired},
		{"open home", "portal", "cursor", ErrInvalidCommand},
		{" ", "admin", "", ErrInvalidCommand},
		{"open\nnodes", "admin", "", ErrInvalidCommand},
		{"open\u2028nodes", "admin", "", ErrInvalidCommand},
		{"open\u200bnodes", "admin", "", ErrInvalidCommand},
		{"open\x00nodes", "admin", "", ErrInvalidCommand},
		{"open \xff", "admin", "", ErrInvalidCommand},
		{"open nodes" + strings.Repeat(" ", MaxCommandBytes), "admin", "", ErrInvalidCommand},
		{"do not open nodes", "admin", "", ErrUnknownCommand},
		{"open nodes; deploy model", "admin", "", ErrUnknownCommand},
		{"open https://example.invalid", "admin", "", ErrUnknownCommand},
		{"confirm plan", "admin", "", ErrUnknownCommand},
	} {
		actor := identity.Auth{Me: identity.Me{Audience: test.audience}}
		got, err := service.Run(t.Context(), actor, test.input, test.cursor)
		if got != (Result{}) || !errors.Is(err, test.err) {
			t.Errorf("input %q in %s: got %#v, %v; want empty result and %v", test.input, test.audience, got, err, test.err)
		}
	}
}
