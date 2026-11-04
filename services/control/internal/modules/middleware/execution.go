package middleware

import (
	"context"
	"encoding/json"
)

type executionEvidence struct {
	FormatVersion    int               `json:"format_version"`
	Kind             string            `json:"kind"`
	Observation      *Observation      `json:"observation,omitempty"`
	MySQLObservation *MySQLObservation `json:"mysql_observation,omitempty"`
}

func (s *Service) execute(ctx context.Context, c *claimed) ([]byte, error) {
	v, e := s.Get(ctx, c.actor, c.WorkspaceID, c.InstanceID)
	if e != nil {
		return nil, e
	}
	if v.State != "active" || v.Version != c.instanceVersion || v.Fingerprint != c.fingerprint {
		return nil, fail("MIDDLEWARE_INPUT_CHANGED")
	}
	p, e := s.profile(v.ProfileKey)
	if e != nil {
		return nil, e
	}
	captured, e := Capture(p)
	if e != nil {
		return nil, e
	}
	if captured.fingerprint != c.fingerprint {
		return nil, fail("MIDDLEWARE_INPUT_CHANGED")
	}
	if c.Kind == "health" {
		if captured.mysql != nil {
			observation, e := ProbeMySQL(ctx, captured, s.configuration.Policy)
			if e != nil {
				return nil, e
			}
			return json.Marshal(executionEvidence{FormatVersion: 1, Kind: "health", MySQLObservation: &observation})
		}
		observation, e := Probe(ctx, captured, s.configuration.Policy)
		if e != nil {
			return nil, e
		}
		return json.Marshal(executionEvidence{FormatVersion: 1, Kind: "health", Observation: &observation})
	}
	return nil, fail("MIDDLEWARE_EXTERNAL_MUTATION_REFUSED")
}
