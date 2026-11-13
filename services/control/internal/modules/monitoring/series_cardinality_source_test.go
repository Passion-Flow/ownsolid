package monitoring

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
)

const testCardinalitySourceID = "66666666-6666-4666-8666-666666666666"
const testCardinalityInstanceID = "77777777-7777-4777-8777-777777777777"

type cardinalityOwnerFunc func(context.Context, string) (SeriesCardinalitySourceLease, error)

func (f cardinalityOwnerFunc) OpenSeriesCardinalitySource(ctx context.Context, source string) (SeriesCardinalitySourceLease, error) {
	return f(ctx, source)
}

type cardinalityRoundTripFunc func(*http.Request) (*http.Response, error)

func (f cardinalityRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type cardinalityTestLease struct {
	observation SeriesCardinalitySourceObservation
	ctx         context.Context
	transport   http.RoundTripper
	close       func() error
}

func (l *cardinalityTestLease) Observation() SeriesCardinalitySourceObservation { return l.observation }
func (l *cardinalityTestLease) Context() context.Context                        { return l.ctx }
func (l *cardinalityTestLease) RoundTrip(r *http.Request) (*http.Response, error) {
	return l.transport.RoundTrip(r)
}
func (l *cardinalityTestLease) Close() error {
	if l.close != nil {
		return l.close()
	}
	return nil
}

func cardinalitySourceConfig() Config {
	c := Defaults()
	c.SeriesCardinality = SeriesCardinalityPolicy{SourceID: testCardinalitySourceID, RawOrigin: "http://127.0.0.1:1", MaximumScanSeries: 10000, LookbackDays: 7, MaximumRanks: 3}
	return c
}

func cardinalitySourceObservation(p SeriesCardinalityPolicy) SeriesCardinalitySourceObservation {
	return SeriesCardinalitySourceObservation{SourceID: p.SourceID, InstanceID: testCardinalityInstanceID, Origin: p.RawOrigin,
		ApprovedExecutableSHA256: strings.Repeat("a", 64), RunningExecutableSHA256: strings.Repeat("a", 64),
		Arguments: []string{"/test/victoria-metrics", "-disablePerDayIndex=false", "-search.maxTSDBStatusSeries=" + strconv.FormatUint(p.MaximumScanSeries, 10),
			"-search.maxTSDBStatusTopNSeries=" + strconv.Itoa(p.MaximumRanks), "-search.maxStatusRequestDuration=1s", "-retentionPeriod=7d"}}
}

func newTestSeriesCardinalityReader(t *testing.T, p SeriesCardinalityPolicy, timeout time.Duration, concurrent int) *seriesCardinalityReader {
	t.Helper()
	p.SourceID, p.MaximumScanSeries = testCardinalitySourceID, 10000
	c := Defaults()
	c.SeriesCardinality, c.OperationSeconds = p, int(timeout/time.Second)
	transport := newMetricsQueryClient(timeout, concurrent).Transport
	t.Cleanup(transport.(*http.Transport).CloseIdleConnections)
	owner := cardinalityOwnerFunc(func(ctx context.Context, id string) (SeriesCardinalitySourceLease, error) {
		if id != p.SourceID {
			t.Fatal("wrong trusted source reference")
		}
		life, cancel := context.WithCancel(t.Context())
		return &cardinalityTestLease{cardinalitySourceObservation(p), life, transport, func() error { cancel(); return nil }}, nil
	})
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	source, err := OpenSeriesCardinalityQuerySource(ctx, c, owner)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	return newSeriesCardinalityReader(p, source)
}

func TestSeriesCardinalitySourceRejectsUntrustedObservation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*cardinalityTestLease)
	}{
		{"missing source", func(l *cardinalityTestLease) { l.observation.SourceID = "" }},
		{"foreign source", func(l *cardinalityTestLease) { l.observation.SourceID = testCardinalityInstanceID }},
		{"missing generation", func(l *cardinalityTestLease) { l.observation.InstanceID = "" }},
		{"foreign origin", func(l *cardinalityTestLease) { l.observation.Origin = "http://127.0.0.1:2" }},
		{"missing approval", func(l *cardinalityTestLease) { l.observation.ApprovedExecutableSHA256 = "" }},
		{"different executable", func(l *cardinalityTestLease) { l.observation.RunningExecutableSHA256 = strings.Repeat("b", 64) }},
		{"uppercase approval", func(l *cardinalityTestLease) {
			l.observation.ApprovedExecutableSHA256 = strings.Repeat("A", 64)
			l.observation.RunningExecutableSHA256 = strings.Repeat("A", 64)
		}},
		{"missing argv", func(l *cardinalityTestLease) { l.observation.Arguments = nil }},
		{"missing lifecycle", func(l *cardinalityTestLease) { l.ctx = nil }},
		{"unbounded lifecycle", func(l *cardinalityTestLease) { l.ctx = context.Background() }},
		{"stopped generation", func(l *cardinalityTestLease) { ctx, cancel := context.WithCancel(t.Context()); cancel(); l.ctx = ctx }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := cardinalitySourceConfig()
			life, cancel := context.WithCancel(t.Context())
			defer cancel()
			var closes, requests int
			lease := &cardinalityTestLease{cardinalitySourceObservation(c.SeriesCardinality), life,
				cardinalityRoundTripFunc(func(*http.Request) (*http.Response, error) { requests++; return nil, errors.New("unexpected I/O") }),
				func() error { closes++; return nil }}
			tc.change(lease)
			ctx, stop := context.WithTimeout(t.Context(), time.Second)
			defer stop()
			source, err := OpenSeriesCardinalityQuerySource(ctx, c, cardinalityOwnerFunc(func(context.Context, string) (SeriesCardinalitySourceLease, error) { return lease, nil }))
			if source != nil || err == nil || err.Error() != "MONITORING_CARDINALITY_UNAVAILABLE" || requests != 0 || closes != 1 {
				t.Fatalf("untrusted observation admitted or leaked a lease: source=%v error=%v requests=%d closes=%d", source, err, requests, closes)
			}
		})
	}
}

func TestSeriesCardinalitySourceNativeProfile(t *testing.T) {
	c := cardinalitySourceConfig()
	s := &SeriesCardinalityQuerySource{policy: c.SeriesCardinality, timeout: 10 * time.Second}
	good := cardinalitySourceObservation(c.SeriesCardinality)
	if !s.admits(good) {
		t.Fatal("rejected canonical observed profile")
	}
	for _, flag := range good.Arguments[1:] {
		name, _, _ := strings.Cut(flag, "=")
		for _, replacement := range []string{"", name, "-" + flag, flag + " ", name + "=%{SETTING}"} {
			t.Run(replacement, func(t *testing.T) {
				o := good
				o.Arguments = append([]string(nil), good.Arguments...)
				for i, arg := range o.Arguments {
					if arg == flag {
						o.Arguments[i] = replacement
					}
				}
				if s.admits(o) {
					t.Fatal("admitted absent or ambiguous required flag")
				}
			})
		}
		duplicate := good
		duplicate.Arguments = append(append([]string(nil), good.Arguments...), flag)
		if s.admits(duplicate) {
			t.Fatal("admitted duplicate required flag")
		}
	}
	for _, flag := range []string{"-disablePerDayIndex=true", "-envflag.enable=true", "--envflag.enable=false", "-envflag.enable", "-search.maxTSDBStatusSeries=10001", "-search.maxStatusRequestDuration=1h", "-retentionPeriod=1M", "-unrelated=%{SETTING}"} {
		o := good
		o.Arguments = append(append([]string(nil), good.Arguments...), flag)
		if s.admits(o) {
			t.Fatalf("admitted override %s", flag)
		}
	}
	for name, replacement := range map[string]string{
		"disablePerDayIndex": "true", "search.maxStatusRequestDuration": "0s", "search.maxTSDBStatusSeries": "9999",
		"search.maxTSDBStatusTopNSeries": "2", "retentionPeriod": "6d",
	} {
		o := good
		o.Arguments = append([]string(nil), good.Arguments...)
		for i, arg := range o.Arguments {
			if strings.HasPrefix(arg, "-"+name+"=") {
				o.Arguments[i] = "-" + name + "=" + replacement
			}
		}
		if s.admits(o) {
			t.Fatalf("admitted insufficient profile %s", name)
		}
	}
	o := good
	o.Arguments = append(append([]string(nil), good.Arguments...), "-envflag.enable=false")
	if !s.admits(o) {
		t.Fatal("explicit disabled env flags should preserve the profile")
	}
}

func TestSeriesCardinalitySourceRequiresCompositionOwner(t *testing.T) {
	c := cardinalitySourceConfig()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	var typedNil cardinalityOwnerFunc
	for _, owner := range []SeriesCardinalitySourceOwner{nil, typedNil} {
		if source, err := OpenSeriesCardinalityQuerySource(ctx, c, owner); source != nil || err == nil {
			t.Fatal("policy alone granted source authority")
		}
	}
	for _, tc := range []struct {
		lease SeriesCardinalitySourceLease
		err   error
	}{
		{nil, nil}, {(*cardinalityTestLease)(nil), nil}, {nil, errors.New("private owner diagnostic")},
	} {
		source, err := OpenSeriesCardinalityQuerySource(ctx, c, cardinalityOwnerFunc(func(context.Context, string) (SeriesCardinalitySourceLease, error) { return tc.lease, tc.err }))
		if source != nil || err == nil || err.Error() != "MONITORING_CARDINALITY_UNAVAILABLE" {
			t.Fatalf("missing owner leaked authority or private error: %v", err)
		}
	}
	var zero SeriesCardinalityQuerySource
	if zero.matches(c) || newSeriesCardinalityReader(c.SeriesCardinality, nil) != nil {
		t.Fatal("zero source enabled raw URL fallback")
	}
	if err := zero.Close(); err != nil {
		t.Fatal(err)
	}
}

type cardinalityTrackedBody struct {
	io.Reader
	closed     *atomic.Int32
	closeError error
}

func (b cardinalityTrackedBody) Close() error { b.closed.Add(1); return b.closeError }

func TestSeriesCardinalitySourceLossRecoveryAndCleanup(t *testing.T) {
	c := cardinalitySourceConfig()
	const body = `{"status":"success","data":{"totalSeries":0,"seriesCountByMetricName":[],"seriesCountByFocusLabelValue":[]}}`
	for _, mode := range []string{"success", "loss during response", "adapter close", "body close failure", "lease close failure", "transport error with body", "nil response", "nil body"} {
		t.Run(mode, func(t *testing.T) {
			var opens, closes, bodies atomic.Int32
			var source *SeriesCardinalityQuerySource
			var requestNumber atomic.Int32
			owner := cardinalityOwnerFunc(func(ctx context.Context, id string) (SeriesCardinalitySourceLease, error) {
				if id != c.SeriesCardinality.SourceID {
					t.Fatal("wrong source")
				}
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("unbounded lease acquisition")
				}
				generation := opens.Add(1)
				life, cancel := context.WithCancel(t.Context())
				obs := cardinalitySourceObservation(c.SeriesCardinality)
				if generation > 2 {
					obs.InstanceID = "88888888-8888-4888-8888-888888888888"
				}
				return &cardinalityTestLease{obs, life, cardinalityRoundTripFunc(func(r *http.Request) (*http.Response, error) {
					n := requestNumber.Add(1)
					if r.Context().Err() != nil {
						t.Error("already cancelled query")
					}
					if n == 1 {
						switch mode {
						case "loss during response":
							cancel()
						case "adapter close":
							_ = source.Close()
						case "nil response":
							return nil, nil
						case "nil body":
							return &http.Response{StatusCode: 200}, nil
						}
					}
					var bodyError error
					if mode == "body close failure" && n == 1 {
						bodyError = errors.New("private body error")
					}
					response := &http.Response{StatusCode: 200, Body: cardinalityTrackedBody{strings.NewReader(body), &bodies, bodyError}}
					if mode == "transport error with body" && n == 1 {
						return response, errors.New("private transport error")
					}
					return response, nil
				}), func() error {
					closes.Add(1)
					cancel()
					if mode == "lease close failure" && generation == 2 {
						return errors.New("private release error")
					}
					return nil
				}}, nil
			})
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			var err error
			source, err = OpenSeriesCardinalityQuerySource(ctx, c, owner)
			if err != nil {
				t.Fatal(err)
			}
			defer source.Close()
			if opens.Load() != 1 || requestNumber.Load() != 0 || closes.Load() != 1 {
				t.Fatal("opening adapter queried store or retained startup lease")
			}
			reader := newSeriesCardinalityReader(c.SeriesCardinality, source)
			day := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
			read := func() (SeriesCardinality, error) {
				return reader.read(ctx, workspaces.Workspace{}, day, SeriesCardinalityQuery{Date: day})
			}
			out, err := read()
			if mode == "success" {
				if err != nil || out.Metrics == nil {
					t.Fatalf("successful lease: %+v %v", out, err)
				}
			} else if err == nil || err.Error() != "MONITORING_CARDINALITY_UNAVAILABLE" || out.Metrics != nil {
				t.Fatalf("lost source released result: %+v %v", out, err)
			}
			if closes.Load() != 2 {
				t.Fatal("query lease not released exactly once")
			}
			wantBodies := int32(1)
			if mode == "nil response" || mode == "nil body" {
				wantBodies = 0
			}
			if bodies.Load() != wantBodies {
				t.Fatal("response body leak")
			}
			if mode != "adapter close" {
				if out, err = read(); err != nil || out.Metrics == nil || opens.Load() != 3 || closes.Load() != 3 {
					t.Fatalf("fresh generation did not recover: %+v %v", out, err)
				}
			}
			_ = source.Close()
			before := opens.Load()
			if _, err = read(); err == nil || opens.Load() != before {
				t.Fatal("closed adapter reacquired authority")
			}
		})
	}
}

func TestSeriesCardinalitySourceCancelsInFlightLease(t *testing.T) {
	for _, mode := range []string{"adapter", "generation", "caller"} {
		t.Run(mode, func(t *testing.T) {
			c := cardinalitySourceConfig()
			started := make(chan struct{})
			var closes atomic.Int32
			var cancelGeneration context.CancelFunc
			owner := cardinalityOwnerFunc(func(context.Context, string) (SeriesCardinalitySourceLease, error) {
				life, cancel := context.WithCancel(t.Context())
				cancelGeneration = cancel
				return &cardinalityTestLease{cardinalitySourceObservation(c.SeriesCardinality), life, cardinalityRoundTripFunc(func(r *http.Request) (*http.Response, error) {
					close(started)
					<-r.Context().Done()
					return nil, r.Context().Err()
				}), func() error { closes.Add(1); cancel(); return nil }}, nil
			})
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			source, err := OpenSeriesCardinalityQuerySource(ctx, c, owner)
			if err != nil {
				t.Fatal(err)
			}
			defer source.Close()
			reader := newSeriesCardinalityReader(c.SeriesCardinality, source)
			done := make(chan error, 1)
			go func() {
				day := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
				_, err := reader.read(ctx, workspaces.Workspace{}, day, SeriesCardinalityQuery{Date: day})
				done <- err
			}()
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("query did not enter transport")
			}
			switch mode {
			case "adapter":
				_ = source.Close()
			case "generation":
				cancelGeneration()
			case "caller":
				cancel()
			}
			// A separate deadline observes cleanup even when the caller is cancelled.
			watch, stop := context.WithTimeout(t.Context(), 5*time.Second)
			defer stop()
			select {
			case err = <-done:
				if closes.Load() != 2 {
					t.Fatal("cancelled query retained its lease")
				}
				if mode == "caller" {
					if !errors.Is(err, context.Canceled) {
						t.Fatal("caller cancellation hidden", err)
					}
				} else if err == nil || err.Error() != "MONITORING_CARDINALITY_UNAVAILABLE" {
					t.Fatal("source cancellation hidden", err)
				}
			case <-watch.Done():
				t.Fatal("query adapter cancellation blocked")
			}
		})
	}
}

func TestSeriesCardinalitySourceStartupAndPolicyBinding(t *testing.T) {
	c := cardinalitySourceConfig()
	reader := newTestSeriesCardinalityReader(t, c.SeriesCardinality, 10*time.Second, 2)
	if !reader.source.matches(c) {
		t.Fatal("matching validated policy refused")
	}
	for _, change := range []func(*Config){
		func(c *Config) { c.OperationSeconds++ },
		func(c *Config) { c.SeriesCardinality.SourceID = testCardinalityInstanceID },
		func(c *Config) { c.SeriesCardinality.RawOrigin = "http://127.0.0.1:2" },
		func(c *Config) { c.SeriesCardinality.MaximumScanSeries++ },
		func(c *Config) { c.SeriesCardinality.MaximumRanks++ },
		func(c *Config) { c.SeriesCardinality.LookbackDays++ },
	} {
		other := c
		change(&other)
		if reader.source.matches(other) {
			t.Fatal("adapter reused under different deployment policy")
		}
	}
	var opens, closes int
	owner := cardinalityOwnerFunc(func(context.Context, string) (SeriesCardinalitySourceLease, error) {
		opens++
		life, cancel := context.WithCancel(t.Context())
		return &cardinalityTestLease{ctx: life, close: func() error { closes++; cancel(); return nil }}, errors.New("private acquisition failure")
	})
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if source, err := OpenSeriesCardinalityQuerySource(ctx, c, owner); source != nil || err == nil || err.Error() != "MONITORING_CARDINALITY_UNAVAILABLE" || opens != 1 || closes != 1 {
		t.Fatal("failed acquisition leaked authority or lease", err)
	}
	if source, err := OpenSeriesCardinalityQuerySource(context.Background(), c, owner); source != nil || err == nil || opens != 1 {
		t.Fatal("unbounded startup acquired source")
	}
	cancel()
	if source, err := OpenSeriesCardinalityQuerySource(ctx, c, owner); source != nil || err != context.Canceled || opens != 1 {
		t.Fatal("cancelled startup acquired source", err)
	}
	_ = reader.source.Close()
	if reader.source.matches(c) {
		t.Fatal("closed adapter remained admissible")
	}
}

func TestSeriesCardinalitySourceRevalidatesEveryLease(t *testing.T) {
	for _, change := range []func(*SeriesCardinalitySourceObservation){
		func(o *SeriesCardinalitySourceObservation) { o.Arguments[1] = "-disablePerDayIndex=true" },
		func(o *SeriesCardinalitySourceObservation) { o.RunningExecutableSHA256 = strings.Repeat("b", 64) },
		func(o *SeriesCardinalitySourceObservation) { o.Origin = "http://127.0.0.1:2" },
		func(o *SeriesCardinalitySourceObservation) { o.SourceID = testCardinalityInstanceID },
	} {
		c := cardinalitySourceConfig()
		var opens, closes, requests int
		owner := cardinalityOwnerFunc(func(context.Context, string) (SeriesCardinalitySourceLease, error) {
			opens++
			o := cardinalitySourceObservation(c.SeriesCardinality)
			if opens == 2 {
				change(&o)
			}
			life, cancel := context.WithCancel(t.Context())
			return &cardinalityTestLease{o, life, cardinalityRoundTripFunc(func(*http.Request) (*http.Response, error) {
				requests++
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"success","data":{"totalSeries":0,"seriesCountByMetricName":[],"seriesCountByFocusLabelValue":[]}}`))}, nil
			}), func() error { closes++; cancel(); return nil }}, nil
		})
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		source, err := OpenSeriesCardinalityQuerySource(ctx, c, owner)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		reader := newSeriesCardinalityReader(c.SeriesCardinality, source)
		day := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
		out, err := reader.read(ctx, workspaces.Workspace{}, day, SeriesCardinalityQuery{Date: day})
		if err == nil || err.Error() != "MONITORING_CARDINALITY_UNAVAILABLE" || out.Metrics != nil || requests != 0 || closes != 2 {
			t.Fatal("changed source used the earlier admission", err)
		}
		out, err = reader.read(ctx, workspaces.Workspace{}, day, SeriesCardinalityQuery{Date: day})
		if err != nil || out.Metrics == nil || requests != 1 || closes != 3 {
			t.Fatal("fresh verified source could not retry", err)
		}
		_ = source.Close()
		cancel()
	}
}
