package agentadmission

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

func TestParseShedLevelReadsEveryNameAndRefusesOthers(t *testing.T) {
	for _, want := range []ShedLevel{ShedNone, ShedBulk, ShedTelemetry, ShedHeartbeats} {
		got, e := ParseShedLevel(want.String())
		if e != nil || got != want {
			t.Fatalf("%s read as %v, %v", want, got, e)
		}
	}
	for _, name := range []string{"", "all", "Bulk"} {
		if _, e := ParseShedLevel(name); e == nil {
			t.Fatalf("%q was accepted", name)
		}
	}
}

func TestEachShedLevelRefusesItsPrioritiesAndNoMore(t *testing.T) {
	refused := map[ShedLevel][]wire.Priority{
		ShedNone:       {},
		ShedBulk:       {wire.PriorityBulk},
		ShedTelemetry:  {wire.PriorityBulk, wire.PriorityTelemetry},
		ShedHeartbeats: {wire.PriorityBulk, wire.PriorityTelemetry, wire.PriorityHeartbeat},
	}
	for level, priorities := range refused {
		c, _, _ := newController(t, testPolicy())
		if e := c.SetShed(level); e != nil {
			t.Fatal(e)
		}
		for _, p := range priorityOrder {
			want := false
			for _, q := range priorities {
				want = want || p == q
			}
			release, e := c.Enter(context.Background(), p)
			var refusal *Refusal
			if got := errors.As(e, &refusal); got != want {
				t.Fatalf("%s at priority %s: refused %v, want %v (%v)", level, p, got, want, e)
			}
			if want {
				if refusal.Code != "AGENT_OVERLOADED" || refusal.RetryAfter != c.policy.Priority(p).RetrySeconds {
					t.Fatalf("%s at priority %s: refusal %+v", level, p, refusal)
				}
				continue
			}
			release()
		}
	}
}

func TestShedRefusesBeforeAnyLimitIsSpentAndLiftingRestoresCalls(t *testing.T) {
	c, _, _ := newController(t, testPolicy())
	request := Request{Call: wire.CallHeartbeat, Address: "192.0.2.1", Node: "node", Credential: "secret"}
	if e := c.SetShed(ShedHeartbeats); e != nil {
		t.Fatal(e)
	}
	for range 100 {
		var refusal *Refusal
		if _, e := c.Admit(context.Background(), request); !errors.As(e, &refusal) || refusal.Code != "AGENT_OVERLOADED" {
			t.Fatalf("a shed call got %v", e)
		}
	}
	if e := c.SetShed(ShedNone); e != nil {
		t.Fatal(e)
	}
	release, e := c.Admit(context.Background(), request)
	if e != nil {
		t.Fatal("the shed calls spent the node's limit:", e)
	}
	release()
}

func TestSetShedRefusesAnUnknownLevel(t *testing.T) {
	c, _, _ := newController(t, testPolicy())
	if e := c.SetShed(ShedHeartbeats + 1); e == nil || c.Shed() != ShedNone {
		t.Fatalf("level %v, error %v", c.Shed(), e)
	}
}

func TestDatabaseSeatsBoundEveryPriorityAndLeaveHeartbeatsASeat(t *testing.T) {
	p := testPolicy()
	p.Seats, p.Heartbeat.MaxSeats, p.Quarantine.MaxSeats = 64, 64, 64
	p.Telemetry.MaxSeats, p.Bulk.MaxSeats = 16, 16
	p.Telemetry.WaitMilliseconds, p.Bulk.WaitMilliseconds = 0, 0
	c, e := New(p, 8, discardLogger())
	if e != nil {
		t.Fatal(e)
	}
	if c.seats.total != 8 {
		t.Fatalf("total %d", c.seats.total)
	}
	var held []func()
	for _, priority := range []wire.Priority{wire.PriorityBulk, wire.PriorityTelemetry} {
		for {
			release, e := c.Enter(context.Background(), priority)
			if e != nil {
				break
			}
			held = append(held, release)
		}
	}
	if len(held) >= 8 {
		t.Fatalf("telemetry and bulk took all %d seats", len(held))
	}
	release, e := c.Enter(context.Background(), wire.PriorityHeartbeat)
	if e != nil {
		t.Fatal("a heartbeat found no seat:", e)
	}
	release()
	for _, free := range held {
		free()
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }
