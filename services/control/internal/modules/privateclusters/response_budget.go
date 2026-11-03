package privateclusters

import "encoding/json"

// publicResponseSize exactly matches the original entrance's default JSON data
// envelope and Encoder.Encode newline, including UTF-8 and HTML escaping.
func publicResponseSize(value any) (int64, error) {
	raw, e := json.Marshal(map[string]any{"data": value})
	if e != nil {
		return 0, fail("PRIVATE_CLUSTER_RESPONSE_INVALID")
	}
	return int64(len(raw)) + 1, nil
}
func (s *Service) responseLimit() (int64, error) {
	if s == nil {
		return 0, fail("PRIVATE_CLUSTER_OWNER_REQUIRED")
	}
	limit := s.MaximumResponseBytes()
	if limit < MinimumBodyBytes || limit > MaximumBodyBytes {
		return 0, fail("PRIVATE_CLUSTER_CONFIGURATION_INVALID")
	}
	return limit, nil
}
func (s *Service) CheckPublicResponse(value any) error {
	limit, e := s.responseLimit()
	if e != nil {
		return e
	}
	size, e := publicResponseSize(value)
	if e != nil {
		return e
	}
	if size > limit {
		return fail("PRIVATE_CLUSTER_RESPONSE_LIMIT")
	}
	return nil
}

type pageBudget struct {
	limit, used int64
	items       int
}

func (s *Service) newPageBudget() (*pageBudget, error) {
	limit, e := s.responseLimit()
	if e != nil {
		return nil, e
	}
	size, e := publicResponseSize(Page[any]{Items: []any{}})
	if e != nil {
		return nil, e
	}
	if size > limit {
		return nil, fail("PRIVATE_CLUSTER_RESPONSE_LIMIT")
	}
	return &pageBudget{limit: limit, used: size}, nil
}
func (b *pageBudget) add(item any) error {
	raw, e := json.Marshal(item)
	if e != nil {
		return fail("PRIVATE_CLUSTER_RESPONSE_INVALID")
	}
	added := int64(len(raw))
	if b.items > 0 {
		added++
	} // JSON array comma.
	if added > b.limit-b.used {
		return fail("PRIVATE_CLUSTER_RESPONSE_LIMIT")
	}
	b.used += added
	b.items++
	return nil
}
func (b *pageBudget) finish(cursor *string) error {
	if cursor == nil {
		return nil
	}
	raw, e := json.Marshal(cursor)
	if e != nil {
		return fail("PRIVATE_CLUSTER_RESPONSE_INVALID")
	}
	// The empty-page envelope already includes the literal null cursor.
	added := int64(len(raw) - len("null"))
	if added > b.limit-b.used {
		return fail("PRIVATE_CLUSTER_RESPONSE_LIMIT")
	}
	return nil
}
