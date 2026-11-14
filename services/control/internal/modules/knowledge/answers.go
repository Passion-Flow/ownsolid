package knowledge

import (
	"context"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

var sourceCitation = regexp.MustCompile(`\[(S[^\]\r\n]*)\]`)

func citedSources(answer string, matches []Match) ([]Match, error) {
	found := sourceCitation.FindAllStringSubmatch(answer, -1)
	if len(found) == 0 || strings.Count(answer, "[S") != len(found) {
		return nil, failure("KNOWLEDGE_INVALID_CITATIONS")
	}
	known := map[string]bool{}
	used := map[string]bool{}
	for _, m := range matches {
		known[m.CitationID] = true
	}
	for _, c := range found {
		if !known[c[1]] {
			return nil, failure("KNOWLEDGE_INVALID_CITATIONS")
		}
		used[c[1]] = true
	}
	out := make([]Match, 0, len(used))
	for _, m := range matches {
		if used[m.CitationID] {
			out = append(out, m)
		}
	}
	return out, nil
}
func boundedContext(matches []Match, maximum int) []Match {
	out := make([]Match, 0, len(matches))
	for _, m := range matches {
		if maximum == 0 {
			break
		}
		if len(m.Text) > maximum {
			end := maximum
			for end > 0 && !utf8.ValidString(m.Text[:end]) {
				end--
			}
			m.Text = m.Text[:end]
			m.ExcerptTruncated = true
		}
		if m.Text == "" {
			break
		}
		out = append(out, m)
		maximum -= len(m.Text)
	}
	return out
}

func (s *Service) Answer(ctx context.Context, a identity.Auth, workspace, base string, input AnswerRequest, request string) (Answer, error) {
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().QuerySeconds))
	defer cancel()
	prepared, e := s.PrepareAnswer(ctx, a, workspace, base, input, request)
	if e != nil {
		return Answer{}, e
	}
	completed, e := s.CompletePreparedAnswer(ctx, a, prepared, "", request)
	if e != nil {
		return Answer{}, e
	}
	return completed.Answer(), nil
}
