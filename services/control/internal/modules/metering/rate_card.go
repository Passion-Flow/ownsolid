package metering

import (
	json "encoding/json/v2"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"
)

// ErrInvalidRateCard indicates a missing, ambiguous or invalid installed rate.
var ErrInvalidRateCard = errors.New("METERING_INVALID_RATE_CARD")

// Meter identifies one ledger field and its exact base unit.
type Meter string

const (
	// GPUMicroseconds counts whole device microseconds.
	GPUMicroseconds Meter = "gpu_microseconds"
	// Tokens counts whole tokens.
	Tokens Meter = "tokens"
	// StorageByteSeconds counts whole byte-seconds.
	StorageByteSeconds Meter = "storage_byte_seconds"
	// Requests counts whole requests.
	Requests Meter = "requests"
)

// MeterRate prices Units of a meter at AmountMicros millionths of the currency.
// Partial quantities use the same linear rate.
type MeterRate struct {
	Units        int64
	AmountMicros int64
}

// RateCard is an immutable, occurrence-rated price history in one currency.
type RateCard struct {
	currency string
	versions []rateVersion
}

type rateVersion struct {
	id        string
	effective time.Time
	rates     map[Meter]MeterRate
}

type rateCardConfig struct {
	Currency string              `json:"currency"`
	Versions []rateVersionConfig `json:"versions"`
}

type rateVersionConfig struct {
	ID          string            `json:"id"`
	EffectiveAt time.Time         `json:"effective_at"`
	Rates       []meterRateConfig `json:"rates"`
}

type meterRateConfig struct {
	Meter        Meter  `json:"meter"`
	Units        int64  `json:"units"`
	AmountMicros *int64 `json:"amount_micros"`
}

var accountingCurrency = regexp.MustCompile(`^[A-Z]{3}$`)

// DecodeRateCard validates the installation's JSON rate history. Each
// version replaces the complete set of rates at its effective instant.
func DecodeRateCard(raw []byte) (*RateCard, error) {
	var config rateCardConfig
	if err := json.Unmarshal(raw, &config, json.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("%w: use one JSON document with unique, declared fields", ErrInvalidRateCard)
	}
	if !accountingCurrency.MatchString(config.Currency) || len(config.Versions) == 0 {
		return nil, fmt.Errorf("%w: provide a three-letter uppercase currency and at least one version", ErrInvalidRateCard)
	}

	card := &RateCard{currency: config.Currency, versions: make([]rateVersion, 0, len(config.Versions))}
	ids := make(map[string]bool, len(config.Versions))
	instants := make(map[time.Time]bool, len(config.Versions))
	for i, input := range config.Versions {
		effective := input.EffectiveAt.UTC().Truncate(time.Microsecond)
		if !eventIdentity.MatchString(input.ID) || ids[input.ID] || instants[effective] ||
			effective.IsZero() || effective.Year() < 1 || effective.Year() > 9999 || !effective.Equal(input.EffectiveAt) {
			return nil, fmt.Errorf("%w: version %d requires a unique ID and a unique microsecond-aligned effective time", ErrInvalidRateCard, i+1)
		}
		if input.Rates == nil {
			return nil, fmt.Errorf("%w: version %d requires an explicit rates array", ErrInvalidRateCard, i+1)
		}
		version := rateVersion{id: input.ID, effective: effective, rates: make(map[Meter]MeterRate, len(input.Rates))}
		for j, rate := range input.Rates {
			if _, duplicate := version.rates[rate.Meter]; duplicate || !rate.Meter.valid() ||
				rate.Units <= 0 || rate.AmountMicros == nil || *rate.AmountMicros < 0 {
				return nil, fmt.Errorf("%w: version %d rate %d requires a unique known meter, positive units and explicit nonnegative amount", ErrInvalidRateCard, i+1, j+1)
			}
			version.rates[rate.Meter] = MeterRate{Units: rate.Units, AmountMicros: *rate.AmountMicros}
		}
		card.versions = append(card.versions, version)
		ids[input.ID], instants[effective] = true, true
	}
	slices.SortFunc(card.versions, func(a, b rateVersion) int { return a.effective.Compare(b.effective) })
	return card, nil
}

func (meter Meter) valid() bool {
	switch meter {
	case GPUMicroseconds, Tokens, StorageByteSeconds, Requests:
		return true
	default:
		return false
	}
}
