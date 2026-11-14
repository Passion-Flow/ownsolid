//go:build telemetry_scale

package monitoringmetrics

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"
)

const (
	scaleCells         = 5
	scaleSeriesPerNode = 1700
	scaleInterval      = 15 * time.Second
	scaleWorkers       = 6
	scaleNodesPerBatch = 10
)

type scaleStore struct {
	origin string
	http   *http.Client
}

func TestVictoriaMetricsFleetScale(t *testing.T) {
	address := os.Getenv("OWNSOLID_SCALE_METRICS_ADDRESS")
	origin, err := url.Parse(address)
	if err != nil || origin.Scheme != "http" || !net.ParseIP(origin.Hostname()).IsLoopback() ||
		origin.Port() == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		t.Fatal("OWNSOLID_SCALE_METRICS_ADDRESS must name a disposable HTTP store on a loopback IP and port")
	}
	nodes := scaleSetting(t, "OWNSOLID_SCALE_NODES", scaleCells, 10_000)
	if nodes%scaleCells != 0 {
		t.Fatalf("node count must be a multiple of %d", scaleCells)
	}
	rounds := scaleSetting(t, "OWNSOLID_SCALE_ROUNDS", 2, 120)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxIdleConnsPerHost = scaleWorkers + 1
	t.Cleanup(transport.CloseIdleConnections)
	store := scaleStore{address, &http.Client{
		Transport: transport,
		Timeout:   2 * time.Minute,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}

	ctx := t.Context()
	initial, err := store.stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if initial.series != 0 || initial.rows != 0 {
		t.Fatalf("load fixture requires a fresh store: already has %d rows and %d new series", initial.rows, initial.series)
	}

	series := nodes * scaleSeriesPerNode
	t.Logf("%d nodes across %d cells, %d series, %d rounds, %s interval", nodes, scaleCells, series, rounds, scaleInterval)
	initialTime := time.Now().Truncate(time.Second)
	start := time.Now()
	if err := store.ingest(ctx, nodes, 0, initialTime); err != nil {
		t.Fatal(err)
	}
	reportIngest(t, 0, series, time.Since(start))

	if err := store.request(ctx, http.MethodGet, "/internal/force_flush", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.measureQueries(t, nodes, initialTime, "first query"); err != nil {
		t.Fatal(err)
	}

	next := initialTime.Add(scaleInterval)
	prior := initialTime
	overruns := 0
	for round := 1; round < rounds; round++ {
		if delay := time.Until(next); delay > 0 {
			time.Sleep(delay)
		}
		start = time.Now()
		stamp := start.Truncate(time.Second)
		queryDone := make(chan error, 1)
		go func() {
			queryDone <- store.measureQueries(t, nodes, prior, "under ingest")
		}()
		ingestErr := store.ingest(ctx, nodes, round, stamp)
		elapsed := time.Since(start)
		if err := errors.Join(ingestErr, <-queryDone); err != nil {
			t.Fatal(err)
		}
		reportIngest(t, round, series, elapsed)
		if elapsed > scaleInterval {
			overruns++
		}
		next = start.Add(scaleInterval)
		prior = stamp
	}

	if err := store.request(ctx, http.MethodGet, "/internal/force_flush", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	final, err := store.awaitStats(ctx, uint64(rounds*series), uint64(series))
	if err != nil {
		t.Fatal(err)
	}
	if final.rows != uint64(rounds*series) || final.series != uint64(series) || final.invalid != initial.invalid {
		t.Fatalf("expected %d rows and %d series with no invalid rows, got %+v", rounds*series, series, final)
	}
	t.Logf("verified %d stored samples across %d series; %d of %d steady rounds missed %s; peak process RSS %d bytes",
		final.rows, final.series, overruns, rounds-1, scaleInterval, final.peakRSS)
	if err := store.measureQueries(t, nodes, prior, "after ingest"); err != nil {
		t.Fatal(err)
	}
	if overruns != 0 {
		t.Errorf("store and generator missed %d of %d steady ingest intervals", overruns, rounds-1)
	}
}

func scaleSetting(t *testing.T, key string, minimum, maximum int) int {
	t.Helper()
	value, err := strconv.Atoi(os.Getenv(key))
	if err != nil || value < minimum || value > maximum {
		t.Fatalf("%s must be an integer between %d and %d", key, minimum, maximum)
	}
	return value
}

func reportIngest(t *testing.T, round, samples int, elapsed time.Duration) {
	t.Helper()
	t.Logf("round %d: %s (%.0f samples/s)", round, elapsed.Round(time.Millisecond), float64(samples)/elapsed.Seconds())
}

func (s scaleStore) ingest(ctx context.Context, nodes, round int, stamp time.Time) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	query := url.Values{"timestamp": {strconv.FormatInt(stamp.UnixMilli(), 10)}}
	var workers sync.WaitGroup
	for worker := range scaleWorkers {
		workers.Go(func() {
			var batch []byte
			for first := worker * scaleNodesPerBatch; first < nodes && ctx.Err() == nil; first += scaleWorkers * scaleNodesPerBatch {
				last := min(first+scaleNodesPerBatch, nodes)
				batch = scaleBatch(batch[:0], first, last, nodes/scaleCells, round)
				err := s.request(ctx, http.MethodPost, "/api/v1/import/prometheus", query, batch, nil)
				if err != nil {
					cancel(fmt.Errorf("ingest round %d nodes %d-%d: %w", round, first, last-1, err))
					return
				}
			}
		})
	}
	workers.Wait()
	return context.Cause(ctx)
}

func scaleBatch(batch []byte, first, last, nodesPerCell, round int) []byte {
	for node := first; node < last; node++ {
		labels := fmt.Sprintf("{cell_id=\"%d\",node_id=\"%d\"} ", node/nodesPerCell, node)
		values := rand.NewPCG(uint64(node), uint64(round))
		for metric := range scaleSeriesPerNode {
			batch = append(batch, "ownsolid_scale_"...)
			batch = strconv.AppendInt(batch, int64(metric), 10)
			batch = append(batch, labels...)
			batch = strconv.AppendUint(batch, values.Uint64()%100_000, 10)
			batch = append(batch, '\n')
		}
	}
	return batch
}

type scaleStats struct {
	series, rows, invalid, peakRSS uint64
}

// VictoriaMetrics caches storage metrics briefly, including immediately after a force flush.
func (s scaleStore) awaitStats(ctx context.Context, rows, series uint64) (scaleStats, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		stats, err := s.stats(ctx)
		if err != nil {
			return stats, err
		}
		if stats.rows >= rows && stats.series >= series {
			return stats, nil
		}
		select {
		case <-ctx.Done():
			return stats, fmt.Errorf("storage counters did not reach %d rows and %d series: got %+v: %w", rows, series, stats, ctx.Err())
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (s scaleStore) stats(ctx context.Context) (scaleStats, error) {
	var stats scaleStats
	keys := map[string]*uint64{
		"vm_new_timeseries_created_total":            &stats.series,
		"vm_rows_added_to_storage_total":             &stats.rows,
		"vm_rows_invalid_total{type=\"prometheus\"}": &stats.invalid,
		"process_resident_memory_peak_bytes":         &stats.peakRSS,
	}
	err := s.request(ctx, http.MethodGet, "/metrics", nil, nil, func(body io.Reader) error {
		scanner := bufio.NewScanner(body)
		for scanner.Scan() {
			line := bytes.Fields(scanner.Bytes())
			if len(line) != 2 {
				continue
			}
			key := string(line[0])
			if dst, ok := keys[key]; ok {
				value, err := strconv.ParseUint(string(line[1]), 10, 64)
				if err != nil {
					return fmt.Errorf("parse %s: %w", key, err)
				}
				*dst = value
				delete(keys, key)
			}
		}
		if err := scanner.Err(); err != nil {
			return err
		}
		for missing := range keys {
			return fmt.Errorf("metrics response is missing %s", missing)
		}
		return nil
	})
	return stats, err
}

func (s scaleStore) measureQueries(t *testing.T, nodes int, stamp time.Time, phase string) error {
	queries := []struct {
		name, expression string
		expected         int
	}{
		{"node", "count({node_id=\"0\",cell_id=\"0\"})", scaleSeriesPerNode},
		{"cell metric", "count(ownsolid_scale_0{cell_id=\"0\"})", nodes / scaleCells},
		{"fleet metric", "count(ownsolid_scale_0)", nodes},
	}
	for _, q := range queries {
		start := time.Now()
		params := url.Values{
			"query": {q.expression}, "time": {stamp.Format(time.RFC3339)}, "nocache": {"1"}, "latency_offset": {"1ms"},
		}
		err := s.request(t.Context(), http.MethodGet, "/api/v1/query", params, nil, func(body io.Reader) error {
			var reply struct {
				Status string
				Data   struct {
					Result []struct{ Value []json.RawMessage }
				}
			}
			if err := json.NewDecoder(body).Decode(&reply); err != nil {
				return err
			}
			results := reply.Data.Result
			if reply.Status != "success" || len(results) != 1 || len(results[0].Value) != 2 {
				return errors.New("expected a successful instant vector with one result")
			}
			if got := string(results[0].Value[1]); got != strconv.Quote(strconv.Itoa(q.expected)) {
				return fmt.Errorf("expected %d series, got %s", q.expected, got)
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("%s %s: %w", phase, q.name, err)
		}
		t.Logf("%s, %s (%d series): %s", phase, q.name, q.expected, time.Since(start).Round(time.Microsecond))
	}
	return nil
}

func (s scaleStore) request(ctx context.Context, method, path string, params url.Values, payload []byte, read func(io.Reader) error) error {
	endpoint := s.origin + path
	if len(params) != 0 {
		endpoint += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "text/plain; version=0.0.4")
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("%s returned HTTP %d: %s", path, resp.StatusCode, message)
	}
	if read != nil {
		return read(resp.Body)
	}
	_, err = io.Copy(io.Discard, resp.Body)
	return err
}
