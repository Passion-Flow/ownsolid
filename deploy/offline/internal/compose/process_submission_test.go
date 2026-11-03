package compose

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestComposeSubmissionUsesActualSubprocessCreationResult(t *testing.T) {
	for _, fault := range []string{"creation", "nonzero", "deadline", "completed"} {
		t.Run(fault, func(t *testing.T) {
			body := "exit 0"
			if fault == "nonzero" {
				body = "printf 'SYNTHETIC_SECRET_VALUE' >&2; exit 19"
			}
			if fault == "deadline" {
				body = "sleep 20"
			}
			c, client := fixture(t, body)
			if fault == "creation" {
				if e := os.WriteFile(c.Compose.DockerBinary, []byte("#!/nonexistent-projectone-test-interpreter\n"), 0700); e != nil {
					t.Fatal(e)
				}
				var e error
				client, e = New(c)
				if e != nil {
					t.Fatal(e)
				}
			}
			client.settings.CommandSeconds = 1
			before := temporaryEntries(t, c.Verification.MountRoot)
			_, submitted, e := client.executeSubmission(context.Background(), nil, nil)
			if submitted != (fault != "creation") || (e == nil) != (fault == "completed") || len(temporaryEntries(t, c.Verification.MountRoot)) != len(before) {
				t.Fatal("incorrect actual submission or cleanup", submitted, e)
			}
			if e != nil && e.Error() == "SYNTHETIC_SECRET_VALUE" {
				t.Fatal("private child output exposed")
			}
		})
	}
}

func TestComposeSubmissionProofBindsWholeAttemptAndCannotBeForgedByText(t *testing.T) {
	c, scope, model, reader := runtimeFixture(t)
	c.settings.Process = nil // Real adapter precheck, before any mutation command.
	recorded, e := c.ObserveRuntime(context.Background(), reader, scope, model)
	if e != nil {
		t.Fatal(e)
	}
	_, e = c.StartControlRuntime(context.Background(), reader, scope, model, recorded)
	if !ProcessNotSubmitted(e, scope, model, recorded, true, []string{"control"}) || ProcessNotSubmitted(errors.New(e.Error()), scope, model, recorded, true, []string{"control"}) {
		t.Fatal("typed proof missing or string forgery accepted", e)
	}
	for _, fault := range []string{"scope", "model", "runtime", "target", "selection"} {
		t.Run(fault, func(t *testing.T) {
			s, m, r, start, selected := scope, model, recorded, true, []string{"control"}
			switch fault {
			case "scope":
				s.ScopeID = "different"
			case "model":
				m = Model{}
			case "runtime":
				r = RuntimeStatus{}
			case "target":
				start = false
			case "selection":
				selected = []string{"postgres"}
			}
			if ProcessNotSubmitted(e, s, m, r, start, selected) {
				t.Fatal("unrelated attempt reused proof")
			}
		})
	}
}
