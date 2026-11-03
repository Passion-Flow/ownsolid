package scope

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

// These storage methods are explicit test doubles; the tagged CLI uses real APIs.
type storageDouble struct {
	*imageDouble
	volume    bool
	utility   bool
	payload   bool
	fault     string
	fired     bool
	creates   int
	utilities int
	puts      int
	removes   int
}

func (s *storageDouble) fail(name string) bool {
	if s.fault == name && !s.fired {
		s.fired = true
		return true
	}
	return false
}
func (s *storageDouble) CreateVolume(_ context.Context, o engine.Ownership) (engine.Volume, error) {
	s.creates++
	s.volume = true
	if s.fail("volume") {
		return engine.Volume{}, errors.New("EXPLICIT_TEST_VOLUME_RESPONSE_LOST")
	}
	return engine.Volume{Name: o.VolumeName()}, nil
}
func (s *storageDouble) InspectVolume(_ context.Context, o engine.Ownership) (engine.Volume, bool, error) {
	return engine.Volume{Name: o.VolumeName()}, s.volume, nil
}
func (s *storageDouble) CreateUtility(context.Context, engine.Ownership, string, int, int) (engine.Utility, error) {
	s.utilities++
	s.utility = true
	if s.fail("utility") {
		return engine.Utility{}, errors.New("EXPLICIT_TEST_UTILITY_RESPONSE_LOST")
	}
	return engine.Utility{ID: strings.Repeat("d", 64)}, nil
}
func (s *storageDouble) InspectUtility(context.Context, engine.Ownership, string, int, int) (engine.Utility, bool, error) {
	return engine.Utility{ID: strings.Repeat("d", 64)}, s.utility, nil
}
func (s *storageDouble) PutConfiguration(context.Context, engine.Ownership, string, int, int, []engine.ConfigurationFile) error {
	s.puts++
	if s.fail("put-before") {
		return errors.New("EXPLICIT_TEST_WRITE_UNKNOWN")
	}
	s.payload = true
	if s.fail("put-after") {
		return errors.New("EXPLICIT_TEST_WRITE_RESPONSE_LOST")
	}
	return nil
}
func (s *storageDouble) InspectConfiguration(context.Context, engine.Ownership, string, int, int, []engine.ConfigurationFile) (engine.ConfigurationObservation, error) {
	status := "observed_empty_owned_volume"
	if s.payload {
		status = "verified_private_volume_payload"
	}
	return engine.ConfigurationObservation{Status: status}, nil
}
func (s *storageDouble) RemoveUtility(context.Context, engine.Ownership, string, int, int) error {
	s.removes++
	if s.fail("remove-before") {
		return errors.New("EXPLICIT_TEST_REMOVE_UNKNOWN")
	}
	s.utility = false
	if s.fail("remove-after") {
		return errors.New("EXPLICIT_TEST_REMOVE_RESPONSE_LOST")
	}
	return nil
}
func storageFixture(t *testing.T) (configuration.Config, *storageDouble) {
	t.Helper()
	c, l, p := preparedInputFixture(t)
	if _, e := prepareInputs(context.Background(), l, p); e != nil {
		l.close()
		t.Fatal(e)
	}
	state := imageState(l.events)
	image := state.completed["image"]
	d := &imageDouble{observation: *state.engine, found: true}
	d.image.ID = image.ManifestDigest
	d.image.OS = "linux"
	d.image.Architecture = "amd64"
	d.image.RootFS.Type = "layers"
	d.image.RootFS.Layers = image.DiffIDs
	l.close()
	return c, &storageDouble{imageDouble: d}
}
func TestStoragePreparationPersistsPhasesAndDoesNotRepeatEffects(t *testing.T) {
	c, s := storageFixture(t)
	result, e := initializeStorage(context.Background(), c, s, false)
	if e != nil || result.RolesPrepared != 1 || result.RolesPending != 0 || !s.volume || s.utility || !s.payload || s.creates != 1 || s.utilities != 1 || s.puts != 1 || s.removes != 1 {
		t.Fatal(result, e, s)
	}
	if _, e = initializeStorage(context.Background(), c, s, false); e != nil || s.creates != 1 || s.puts != 1 {
		t.Fatal("prepared effects repeated", e)
	}
	status, e := Read(context.Background(), c)
	if e != nil || status.Status != "isolated_scope_storage_payloads_prepared" || status.VolumesPrepared != 1 {
		t.Fatal(status, e)
	}
}
func TestStorageUncertainPhasesRequireObservedResolutionWithoutReplay(t *testing.T) {
	for _, fault := range []string{"volume", "utility", "put-before", "put-after", "remove-before", "remove-after"} {
		t.Run(fault, func(t *testing.T) {
			c, s := storageFixture(t)
			s.fault = fault
			result, e := initializeStorage(context.Background(), c, s, false)
			if e == nil || result.RolesPending != 1 {
				t.Fatal(result, e)
			}
			creates, utilities, puts, removes := s.creates, s.utilities, s.puts, s.removes
			if _, e = initializeStorage(context.Background(), c, s, false); e == nil || s.creates != creates || s.utilities != utilities || s.puts != puts || s.removes != removes {
				t.Fatal("uncertain effect resent", e)
			}
			result, e = initializeStorage(context.Background(), c, s, true)
			if fault == "put-before" || fault == "remove-before" {
				if e == nil || result.RolesPending != 1 {
					t.Fatal("absence/in-flight state falsely resolved", result, e)
				}
				// Explicit test-only later outcome observation, never a production resend.
				if fault == "put-before" {
					s.payload = true
				} else {
					s.utility = false
				}
				result, e = initializeStorage(context.Background(), c, s, true)
			}
			if e != nil || result.RolesPending != 0 {
				t.Fatal(result, e)
			}
			result, e = initializeStorage(context.Background(), c, s, false)
			if e != nil || result.RolesPrepared != 1 || s.creates != 1 || s.utilities != 1 || s.puts > 1 || s.removes > 1 {
				t.Fatal("resolution replayed an effect", result, e, s)
			}
		})
	}
}
