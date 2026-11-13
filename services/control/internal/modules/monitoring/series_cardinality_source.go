package monitoring

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// SeriesCardinalitySourceObservation is an immutable observation of one running
// native source. The approved digest comes from the installed source inventory;
// the running digest and argv come from the observed process, not query policy.
// InstanceID changes whenever that process, profile or endpoint binding changes.
// These values are internal composition data and must never be returned to users.
type SeriesCardinalitySourceObservation struct {
	SourceID                 string
	InstanceID               string
	Origin                   string
	ApprovedExecutableSHA256 string
	RunningExecutableSHA256  string
	Arguments                []string
}

// SeriesCardinalitySourceLease holds the observed generation and endpoint until
// Close. Context is live, cancellable and cancelled on authority, process or
// endpoint loss. RoundTrip must honor request cancellation and target only the
// bound source, without ambient proxies, redirects or DNS destination escape.
// Close releases this query lease promptly; it must not stop the native process.
// Implementations must support concurrent independent leases.
type SeriesCardinalitySourceLease interface {
	http.RoundTripper
	Observation() SeriesCardinalitySourceObservation
	Context() context.Context
	Close() error
}

// SeriesCardinalitySourceOwner is supplied by the trusted runtime/startup owner
// for the installed cell. It resolves sourceID against authenticated installed
// inventory and a real running process. It must never manufacture observations
// from monitoring policy or request input. Acquisition obeys ctx; replacements
// invalidate existing leases before admitting a new generation.
type SeriesCardinalitySourceOwner interface {
	OpenSeriesCardinalitySource(ctx context.Context, sourceID string) (SeriesCardinalitySourceLease, error)
}

// SeriesCardinalityQuerySource admits a trusted owner to the bounded query path.
// Its zero value grants no authority. Close cancels queries and releases adapter
// authority; native producer shutdown remains with the runtime owner.
type SeriesCardinalityQuerySource struct {
	owner   SeriesCardinalitySourceOwner
	policy  SeriesCardinalityPolicy
	timeout time.Duration
	life    context.Context
	cancel  context.CancelFunc
}

// OpenSeriesCardinalityQuerySource checks an actual source lease without making
// a store request. ctx needs a startup deadline. The returned adapter has its own
// lifetime: the startup context may end after this call. Root must call Close on
// constructor failure and shutdown. Every later query reacquires and validates
// the current generation, allowing recovery after a verified source restart.
func OpenSeriesCardinalityQuerySource(ctx context.Context, config Config, owner SeriesCardinalitySourceOwner) (*SeriesCardinalityQuerySource, error) {
	if ctx == nil || config.Validate() != nil || config.SeriesCardinality == (SeriesCardinalityPolicy{}) || nilSource(owner) {
		return nil, errors.New("MONITORING_CARDINALITY_SOURCE_REQUIRED")
	}
	if _, ok := ctx.Deadline(); !ok {
		return nil, errors.New("MONITORING_CARDINALITY_SOURCE_REQUIRED")
	}
	life, cancel := context.WithCancel(context.Background())
	s := &SeriesCardinalityQuerySource{owner, config.SeriesCardinality, time.Duration(config.OperationSeconds) * time.Second, life, cancel}
	if err := s.withLease(ctx, func(context.Context, SeriesCardinalitySourceLease) error { return nil }); err != nil {
		cancel()
		return nil, err
	}
	return s, nil
}

// Close is idempotent and safe during concurrent queries. It does not close or
// stop the runtime owner or any native source process.
func (s *SeriesCardinalityQuerySource) Close() error {
	if s != nil && s.cancel != nil {
		s.cancel()
	}
	return nil
}

func nilSource(v any) bool {
	if v == nil {
		return true
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return r.IsNil()
	}
	return false
}

func (s *SeriesCardinalityQuerySource) matches(config Config) bool {
	return s != nil && s.life != nil && s.life.Err() == nil && s.cancel != nil && !nilSource(s.owner) &&
		s.policy == config.SeriesCardinality && s.timeout == time.Duration(config.OperationSeconds)*time.Second
}

func (s *SeriesCardinalityQuerySource) withLease(ctx context.Context, read func(context.Context, SeriesCardinalitySourceLease) error) (err error) {
	if ctx == nil || s == nil || s.life == nil || s.cancel == nil || nilSource(s.owner) || read == nil {
		return errors.New("MONITORING_CARDINALITY_SOURCE_REQUIRED")
	}
	if ctx.Err() != nil || s.life.Err() != nil {
		return cardinalitySourceError(ctx)
	}
	operation, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	stopLife := context.AfterFunc(s.life, cancel)
	defer stopLife()
	lease, err := s.owner.OpenSeriesCardinalitySource(operation, s.policy.SourceID)
	if nilSource(lease) {
		return cardinalitySourceError(ctx)
	}
	var leaseContext context.Context
	defer func() {
		// Inspect the actual contexts, not callback scheduling. Close may cancel
		// the lease as part of normal release, so validate before releasing it.
		if operation.Err() != nil || s.life.Err() != nil || !nilSource(leaseContext) && leaseContext.Err() != nil {
			err = cardinalitySourceError(ctx)
		}
		if closeError := lease.Close(); closeError != nil {
			err = cardinalitySourceError(ctx)
		}
	}()
	if err != nil {
		return cardinalitySourceError(ctx)
	}
	leaseContext = lease.Context()
	if nilSource(leaseContext) || leaseContext.Done() == nil || leaseContext.Err() != nil ||
		!s.admits(lease.Observation()) || operation.Err() != nil || s.life.Err() != nil {
		return cardinalitySourceError(ctx)
	}
	stopLease := context.AfterFunc(leaseContext, cancel)
	defer stopLease()
	return read(operation, lease)
}

func executableDigest(s string) bool {
	return len(s) == 64 && strings.Trim(s, "0123456789abcdef") == ""
}

func (s *SeriesCardinalityQuerySource) admits(o SeriesCardinalitySourceObservation) bool {
	if o.SourceID != s.policy.SourceID || !validID(o.InstanceID) || o.Origin != s.policy.RawOrigin ||
		!executableDigest(o.ApprovedExecutableSHA256) || o.RunningExecutableSHA256 != o.ApprovedExecutableSHA256 || len(o.Arguments) < 2 || o.Arguments[0] == "" {
		return false
	}
	// Accept one explicit canonical form for each safety-critical flag. Unknown
	// native flags stay the runtime owner's responsibility, but environment
	// templates and flag-loading overrides cannot change this observed profile.
	profile := map[string]string{}
	for _, arg := range o.Arguments[1:] {
		if strings.Contains(arg, "%{") || strings.ContainsRune(arg, 0) {
			return false
		}
		name, value, equal := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		switch name {
		case "disablePerDayIndex", "search.maxTSDBStatusSeries", "search.maxTSDBStatusTopNSeries", "search.maxStatusRequestDuration", "retentionPeriod", "envflag.enable":
			if _, seen := profile[name]; seen || !equal || arg != "-"+name+"="+value {
				return false
			}
			profile[name] = value
		}
	}
	if profile["disablePerDayIndex"] != "false" || profile["search.maxTSDBStatusSeries"] != strconv.FormatUint(s.policy.MaximumScanSeries, 10) {
		return false
	}
	if value, set := profile["envflag.enable"]; set && value != "false" {
		return false
	}
	ranks, err := strconv.Atoi(profile["search.maxTSDBStatusTopNSeries"])
	if err != nil || ranks < s.policy.MaximumRanks || ranks > maximumCardinalityRanks || strconv.Itoa(ranks) != profile["search.maxTSDBStatusTopNSeries"] {
		return false
	}
	duration, err := time.ParseDuration(profile["search.maxStatusRequestDuration"])
	if err != nil || duration <= 0 || duration > s.timeout {
		return false
	}
	days := strings.TrimSuffix(profile["retentionPeriod"], "d")
	retention, err := strconv.Atoi(days)
	return err == nil && retention >= s.policy.LookbackDays && retention <= 366 &&
		strconv.Itoa(retention)+"d" == profile["retentionPeriod"]
}
