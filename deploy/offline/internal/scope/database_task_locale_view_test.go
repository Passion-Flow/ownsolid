package scope

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

type localeConsumerViewDouble struct {
	runtimeEngine
	owner     engine.Ownership
	consumers []string
	forwarded []string
}

func (d *localeConsumerViewDouble) VerifyVolumeConsumers(_ context.Context, owner engine.Ownership, allowed []string) error {
	d.forwarded = append([]string{}, allowed...)
	if owner != d.owner {
		return nil
	}
	for _, actual := range d.consumers {
		found := false
		for _, id := range allowed {
			found = found || actual == id
		}
		if !found {
			return errors.New("OFFLINE_ENGINE_FOREIGN_VOLUME_CONSUMER")
		}
	}
	return nil
}

// Exact validated task metadata fixture; the production SDK remains responsible
// for independent actor/mount/locale ownership validation before constructing it.
func TestDatabaseTaskViewCarriesOnlyItsValidatedLocaleConsumer(t *testing.T) {
	scopeID, operation := strings.Repeat("a", 32), strings.Repeat("b", 32)
	controlID, taskID, foreignID := strings.Repeat("c", 64), strings.Repeat("d", 64), strings.Repeat("e", 64)
	locale := engine.Ownership{ScopeID: scopeID, OperationID: operation, Role: engine.LocalizationConfigurationRole}
	for _, fault := range []string{"exact-locale", "input", "output", "nil-locale", "other-scope", "other-operation", "other-role", "foreign-consumer", "other-volume"} {
		t.Run(fault, func(t *testing.T) {
			input := locale
			input.Role = "operator-config"
			output := locale
			output.Role = "datasets"
			owner := locale
			task := engine.DatabaseTask{Owner: engine.Ownership{ScopeID: scopeID}, VolumeOwner: input, OutputOwner: &output, LocalizationOwner: &locale}
			consumers := []string{controlID, taskID}
			switch fault {
			case "input":
				owner = input
			case "output":
				owner = output
			case "nil-locale":
				task.LocalizationOwner = nil
			case "other-scope":
				owner.ScopeID = strings.Repeat("f", 32)
			case "other-operation":
				owner.OperationID = strings.Repeat("f", 32)
			case "other-role":
				owner.Role = "unselected-locale"
			case "foreign-consumer":
				consumers = append(consumers, foreignID)
			case "other-volume":
				owner.Role = "models"
			}
			d := &localeConsumerViewDouble{owner: owner, consumers: consumers}
			view := databaseTaskRuntimeView{runtimeEngine: d, task: task, object: engine.RuntimeContainer{ID: taskID}}
			allowed := make([]string, 1, 4)
			allowed[0] = controlID
			err := view.VerifyVolumeConsumers(context.Background(), owner, allowed)
			selected := fault == "exact-locale" || fault == "input" || fault == "output" || fault == "foreign-consumer"
			want := []string{controlID}
			if selected {
				want = append(want, taskID)
			}
			if !reflect.DeepEqual(d.forwarded, want) || len(allowed) != 1 || allowed[0] != controlID {
				t.Fatal("changed caller or unrelated-owner allowance", d.forwarded, allowed)
			}
			pass := fault == "exact-locale" || fault == "input" || fault == "output"
			if (err == nil) != pass {
				t.Fatal("consumer admission", fault, err)
			}
		})
	}
}
