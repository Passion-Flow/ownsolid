package identity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
)

func TestRateInputValidationPrecedesStorage(t *testing.T) {
	s := &Service{HMACKey: bytes.Repeat([]byte{7}, security.MinimumHMACKeyBytes)}
	valid := Limit{Purpose: "synthetic", Identifier: "sensitive-subject-do-not-log", Count: 2, Window: time.Minute}
	for name, value := range map[string]Limit{
		"empty_purpose":     {Identifier: valid.Identifier, Count: 2, Window: time.Minute},
		"zero_count":        {Purpose: valid.Purpose, Count: 0, Window: time.Minute},
		"overflow_count":    {Purpose: valid.Purpose, Count: maximumRateAttempts + 1, Window: time.Minute},
		"zero_window":       {Purpose: valid.Purpose, Count: 2},
		"precision":         {Purpose: valid.Purpose, Count: 2, Window: time.Nanosecond},
		"negative_cooldown": {Purpose: valid.Purpose, Count: 2, Window: time.Minute, Cooldown: -time.Second},
		"long_cooldown":     {Purpose: valid.Purpose, Count: 2, Window: time.Minute, Cooldown: 2 * time.Minute},
	} {
		t.Run(name, func(t *testing.T) {
			_, e := s.Admit(context.Background(), value)
			if e == nil || e.Error() != "IDENTITY_RATE_LIMIT_INVALID" || strings.Contains(e.Error(), valid.Identifier) {
				t.Fatal("invalid limit reached storage or leaked input", e)
			}
		})
	}
	if _, e := s.Admit(context.Background(), valid, valid); e == nil || e.Error() != "IDENTITY_RATE_DUPLICATE_BUCKET" {
		t.Fatal("duplicate bucket accepted", e)
	}
	for _, size := range []int{0, security.MinimumHMACKeyBytes - 1, security.MaximumHMACKeyBytes + 1} {
		s.HMACKey = make([]byte, size)
		if _, e := s.Admit(context.Background(), valid); e == nil || e.Error() != "IDENTITY_RATE_KEY_INVALID" {
			t.Fatal("invalid key reached storage", size, e)
		}
	}
}

func TestRatePurposeFramingAndOrdering(t *testing.T) {
	for _, size := range []int{security.MinimumHMACKeyBytes, security.MaximumHMACKeyBytes} {
		s := &Service{HMACKey: bytes.Repeat([]byte{7}, size)}
		limits := []Limit{{Purpose: "a", Identifier: "bc", Count: 1, Window: time.Minute}, {Purpose: "ab", Identifier: "c", Count: 1, Window: time.Minute}}
		items, e := s.rateItems(limits)
		if e != nil || len(items) != 2 || bytes.Compare(items[0].key, items[1].key) >= 0 || len(items[0].key) != sha256.Size {
			t.Fatal("rate key framing/order is ambiguous", e)
		}
		reversed, e := s.rateItems([]Limit{limits[1], limits[0]})
		if e != nil || !bytes.Equal(items[0].key, reversed[0].key) || !bytes.Equal(items[1].key, reversed[1].key) {
			t.Fatal("caller input order changed lock order", e)
		}
	}
}
