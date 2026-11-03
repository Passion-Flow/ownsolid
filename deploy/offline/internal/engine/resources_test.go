package engine

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"testing"
)

func resourceFixture(t *testing.T, fault string) *Client {
	t.Helper()
	id := strings.Repeat("a", 64)
	c, _ := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("resource inspection mutated state")
		}
		write := func(value any) { json.NewEncoder(w).Encode(value) }
		if fault == "malformed" {
			w.Write([]byte("{"))
			return
		}
		switch r.URL.Path {
		case "/v1.49/info":
			memory := int64(10000)
			cpus := int64(10)
			if fault == "capacity" {
				memory = 0
			}
			write(map[string]int64{"MemTotal": memory, "NCPU": cpus})
		case "/v1.49/containers/json":
			if r.URL.Query().Get("all") != "" {
				t.Error("stopped containers included in running reservation")
			}
			if fault == "null-inventory" {
				w.Write([]byte("null"))
				return
			}
			object := map[string]string{"Id": id, "State": "running"}
			if fault == "unknown-id" {
				object["Id"] = "bad"
			}
			if fault == "stopped" {
				object["State"] = "exited"
			}
			result := []any{object}
			if fault == "duplicate" {
				result = append(result, object)
			}
			write(result)
		case "/v1.49/containers/" + id + "/json":
			memory := int64(1000)
			cpu := int64(1000000000)
			running := true
			switch fault {
			case "unbounded-memory":
				memory = 0
			case "unbounded-cpu":
				cpu = 0
			case "negative":
				memory = -1
			case "changed":
				running = false
			case "overflow":
				memory = math.MaxInt64
			}
			write(map[string]any{"Id": id, "State": map[string]any{"Running": running}, "HostConfig": map[string]int64{"Memory": memory, "NanoCpus": cpu}})
		default:
			t.Error("unexpected resource read path", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	return c
}
func generousHost(context.Context, int64) (HostResources, error) {
	return HostResources{MemoryTotalBytes: 10000, MemoryAvailableBytes: 9000, LogicalCPUs: 10, LoadOneMinute: 1, Source: "explicit_test_counter"}, nil
}
func TestResourceAdmissionExactHeadroomAndRefusals(t *testing.T) {
	for _, test := range []struct {
		name       string
		need       ResourceRequirement
		reserve    int
		hostMemory int64
		load       float64
		pass       bool
	}{
		{"exact-floor", ResourceRequirement{6000, 6000}, 30, 9000, 1, true},
		{"engine-memory", ResourceRequirement{6001, 100}, 30, 9000, 1, false},
		{"engine-cpu", ResourceRequirement{100, 6001}, 30, 9000, 1, false},
		{"host-memory", ResourceRequirement{6000, 100}, 30, 8999, 1, false},
		{"host-cpu", ResourceRequirement{100, 6000}, 30, 9000, 1.001, false},
		{"stricter-nondefault", ResourceRequirement{4000, 4000}, 40, 9000, 1, true},
		{"disabled-floor", ResourceRequirement{100, 100}, 29, 9000, 1, false},
		{"invalid-high", ResourceRequirement{100, 100}, 91, 9000, 1, false},
		{"zero-memory", ResourceRequirement{0, 100}, 30, 9000, 1, false},
		{"negative-cpu", ResourceRequirement{100, -1}, 30, 9000, 1, false},
		{"nonfinite-load", ResourceRequirement{100, 100}, 30, 9000, math.NaN(), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := resourceFixture(t, "none")
			host := func(context.Context, int64) (HostResources, error) {
				h, _ := generousHost(context.Background(), 0)
				h.MemoryAvailableBytes = test.hostMemory
				h.LoadOneMinute = test.load
				return h, nil
			}
			result, e := c.admitRuntimeResources(context.Background(), test.need, test.reserve, host)
			if (e == nil) != test.pass {
				t.Fatal(result, e)
			}
			if test.pass && (result.Status != "passed_point_in_time_resource_admission" || result.RunningReservations.MemoryBytes != 1000 || result.RunningReservations.MilliCPU != 1000) {
				t.Fatal(result)
			}
		})
	}
}
func TestResourceMetadataFailuresNeverAdmit(t *testing.T) {
	for _, fault := range []string{"malformed", "capacity", "null-inventory", "unknown-id", "duplicate", "stopped", "unbounded-memory", "unbounded-cpu", "negative", "changed", "overflow"} {
		t.Run(fault, func(t *testing.T) {
			c := resourceFixture(t, fault)
			if _, e := c.admitRuntimeResources(context.Background(), ResourceRequirement{100, 100}, 30, generousHost); e == nil {
				t.Fatal("invalid resources admitted")
			}
		})
	}
	c := resourceFixture(t, "none")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := c.admitRuntimeResources(ctx, ResourceRequirement{100, 100}, 30, generousHost); e == nil {
		t.Fatal("canceled inspection admitted")
	}
}
func TestLinuxMemoryAndLoadCounterParsing(t *testing.T) {
	total, available, e := parseLinuxMemory("MemTotal: 10000 kB\nMemFree: 1 kB\nMemAvailable: 9000 kB\n")
	if e != nil || total != 10240000 || available != 9216000 {
		t.Fatal(total, available, e)
	}
	for _, raw := range []string{"MemTotal: 1 kB\n", "MemTotal: 1 kB\nMemAvailable: 2 kB\n", "MemTotal: 1 bytes\nMemAvailable: 1 kB\n", "MemTotal: 1 kB\nMemTotal: 1 kB\nMemAvailable: 1 kB\n", "MemTotal: 9999999999999999999 kB\nMemAvailable: 1 kB\n"} {
		if _, _, e := parseLinuxMemory(raw); e == nil {
			t.Fatal("invalid memory counter accepted")
		}
	}
	for _, raw := range []string{"1.2 2.3 3.4 1/100 900", "{ 1.2 2.3 3.4 }"} {
		if load, e := parseLoad(raw); e != nil || load != 1.2 {
			t.Fatal(load, e)
		}
	}
	for _, raw := range []string{"", "1.2", "NaN 0 0", "+Inf 0 0", "-1 0 0", "1 2 x", "1 2 3 garbage", "1 2 3 2/1 10", "1 2 3 1/10 pid"} {
		if _, e := parseLoad(raw); e == nil {
			t.Fatal("invalid load counter accepted")
		}
	}
}

func TestRecordedAdmissionRejectsCorruptSnapshot(t *testing.T) {
	c := resourceFixture(t, "none")
	record, e := c.admitRuntimeResources(context.Background(), ResourceRequirement{100, 100}, 30, generousHost)
	if e != nil || record.ValidateRecorded() != nil {
		t.Fatal(record, e)
	}
	for _, fault := range []string{"time", "host", "engine", "requirement", "reservation", "reserve", "status"} {
		t.Run(fault, func(t *testing.T) {
			copy := record
			switch fault {
			case "time":
				copy.ObservedAt = "invalid"
			case "host":
				copy.Host.MemoryAvailableBytes = 0
			case "engine":
				copy.EngineCapacity.MilliCPU = 1
			case "requirement":
				copy.Requirement.MemoryBytes = 0
			case "reservation":
				copy.RunningReservations.MemoryBytes = -1
			case "reserve":
				copy.ReservePercent = 29
			case "status":
				copy.Status = "unknown"
			}
			if copy.ValidateRecorded() == nil {
				t.Fatal("corrupt historical snapshot accepted")
			}
		})
	}
}
