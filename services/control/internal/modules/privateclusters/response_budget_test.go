package privateclusters

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPrivateClusterExactEncodedResponseBudget(t *testing.T) {
	value := map[string]string{"message": "\u4e16\u754c" + strings.Repeat("<&>", 1000)}
	raw, e := json.Marshal(map[string]any{"data": value})
	if e != nil {
		t.Fatal(e)
	}
	s := &Service{config: syntheticConfig()}
	s.config.Policy.MaximumBodyBytes = int64(len(raw)) + 1
	if s.MaximumResponseBytes() <= MinimumBodyBytes || s.CheckPublicResponse(value) != nil {
		t.Fatal("exact nondefault escaped UTF-8/newline budget refused")
	}
	s.config.Policy.MaximumBodyBytes--
	if e = s.CheckPublicResponse(value); e == nil || e.Error() != "PRIVATE_CLUSTER_RESPONSE_LIMIT" {
		t.Fatal("final newline omitted from budget", e)
	}
	for _, limit := range []int64{MinimumBodyBytes, MaximumBodyBytes} {
		s.config.Policy.MaximumBodyBytes = limit
		if s.MaximumResponseBytes() != limit || s.CheckPublicResponse(map[string]string{"value": "small synthetic projection"}) != nil {
			t.Fatal("valid configured boundary refused", limit)
		}
	}
}

func TestPrivateClusterPageBudgetCountsItemsAndCursor(t *testing.T) {
	items := []string{strings.Repeat("a", 2800), strings.Repeat("<", 500)}
	cursor := strings.Repeat("c", 128)
	value := Page[string]{Items: items, NextCursor: &cursor}
	size, e := publicResponseSize(value)
	if e != nil {
		t.Fatal(e)
	}
	s := &Service{config: syntheticConfig()}
	s.config.Policy.MaximumBodyBytes = size
	b, e := s.newPageBudget()
	if e != nil {
		t.Fatal(e)
	}
	for _, item := range items {
		if e = b.add(item); e != nil {
			t.Fatal("exact item byte budget", e)
		}
	}
	if e = b.finish(&cursor); e != nil || s.CheckPublicResponse(value) != nil {
		t.Fatal("exact comma/cursor/envelope refused", e)
	}
	before := b.used
	if e = b.add(strings.Repeat("x", 512)); e == nil || e.Error() != "PRIVATE_CLUSTER_RESPONSE_LIMIT" || b.used != before {
		t.Fatal("overflow retained an extra item", e)
	}
	if e = b.finish(&cursor); e != nil {
		t.Fatal("rejected item changed original accounting", e)
	}
	// A nonempty next cursor replaces null and consumes its exact encoded bytes.
	s.config.Policy.MaximumBodyBytes = size - 1
	b, e = s.newPageBudget()
	if e != nil {
		t.Fatal(e)
	}
	for _, item := range items {
		if e = b.add(item); e != nil {
			t.Fatal(e)
		}
	}
	if e = b.finish(&cursor); e == nil || e.Error() != "PRIVATE_CLUSTER_RESPONSE_LIMIT" {
		t.Fatal("cursor byte overflow accepted", e)
	}
	if e = b.finish(nil); e != nil {
		t.Fatal("absent cursor charged invented bytes", e)
	}
}

func TestPrivateClusterResponseBudgetRefusesInvalidOwnerAndValues(t *testing.T) {
	var missing *Service
	if e := missing.CheckPublicResponse(nil); e == nil || e.Error() != "PRIVATE_CLUSTER_OWNER_REQUIRED" || missing.MaximumResponseBytes() != 0 {
		t.Fatal("missing owner admitted", e)
	}
	s := &Service{config: syntheticConfig()}
	for _, limit := range []int64{0, MinimumBodyBytes - 1, MaximumBodyBytes + 1} {
		s.config.Policy.MaximumBodyBytes = limit
		if e := s.CheckPublicResponse(nil); e == nil || e.Error() != "PRIVATE_CLUSTER_CONFIGURATION_INVALID" {
			t.Fatal("invalid policy admitted", limit, e)
		}
	}
	s.config.Policy.MaximumBodyBytes = MinimumBodyBytes
	if e := s.CheckPublicResponse(func() {}); e == nil || e.Error() != "PRIVATE_CLUSTER_RESPONSE_INVALID" {
		t.Fatal("unencodable response admitted", e)
	}
	b, e := s.newPageBudget()
	if e != nil {
		t.Fatal(e)
	}
	if e = b.add(func() {}); e == nil || e.Error() != "PRIVATE_CLUSTER_RESPONSE_INVALID" {
		t.Fatal("unencodable page item admitted", e)
	}
}
