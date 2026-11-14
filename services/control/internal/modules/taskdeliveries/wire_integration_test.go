//go:build repository_integration && task_events_integration && task_events_wire_integration

package taskdeliveries

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/taskbus"
)

func TestActualTaskEventsWire(t *testing.T) {
	for _, mode := range []string{"publish_ack_lost", "publish_send_cut", "ack_confirmation_lost", "ack_send_cut"} {
		t.Run(mode, func(t *testing.T) {
			var proxy *taskbus.WireFixture
			var endpoint string
			record := func(phase string, observation *taskbus.WireObservation) {
				raw, e := json.Marshal(map[string]any{"phase": phase, "mode": mode, "endpoint": endpoint, "observation": observation})
				if e != nil {
					t.Fatal(e)
				}
				t.Log("TASK_EVENTS_WIRE_EVIDENCE " + string(raw))
			}
			g := eventSetupWithTransport(t, func(ctx context.Context, c taskbus.Configuration) taskbus.Configuration {
				root, e := filepath.EvalSymlinks(t.TempDir())
				if e != nil {
					t.Fatal(e)
				}
				state, e := filepath.EvalSymlinks(os.Getenv("PROJECT_ONE_STATE_ROOT"))
				if e != nil || !strings.HasPrefix(root, state+string(os.PathSeparator)) {
					t.Fatal("OWNED_EXTERNAL_WIRE_ROOT_REQUIRED")
				}
				if e = os.Chmod(root, 0700); e != nil {
					t.Fatal(e)
				}
				var selected taskbus.Configuration
				proxy, selected, e = taskbus.NewWireFixture(ctx, c, root)
				if e != nil {
					t.Fatal("real private upstream wire fixture", e)
				}
				endpoint = selected.Endpoint
				t.Cleanup(func() {
					stop, end := context.WithTimeout(context.Background(), time.Duration(c.ShutdownSeconds)*time.Second)
					defer end()
					if e := proxy.Close(stop); e != nil {
						t.Error("wire fixture join", e)
						return
					}
					connection, e := net.DialTimeout("tcp", strings.TrimPrefix(endpoint, "tls://"), time.Duration(c.ConnectSeconds)*time.Second)
					if e == nil {
						connection.Close()
						t.Error("wire fixture listener remains open")
						return
					}
					record("closed", nil)
				})
				return selected
			})
			_, value := g.task(t)
			if strings.HasPrefix(mode, "publish_") {
				if e := proxy.Arm(mode); e != nil {
					t.Fatal(e)
				}
				if worked, e := g.s.ProcessNext(g.ctx); e == nil || worked {
					t.Fatal("lost/cut publication returned success", worked, e)
				}
				var unknown bool
				var state string
				if e := g.f.Store.QueryRow(g.ctx, g.f.Statement("SELECT state,outcome_unknown FROM task_event_outbox WHERE id=$1", "SELECT state,outcome_unknown FROM task_event_outbox WHERE id=?", value.ID)).Scan(&state, &unknown); e != nil || state != "sent" || !unknown {
					t.Fatal("uncertain publication was not retained", e)
				}
				if e := g.s.Transport.Reconnect(g.ctx); e != nil {
					t.Fatal(e)
				}
				waitEventTime(t, g.ctx, time.Now().Add(time.Duration(g.s.Configuration.RetrySeconds)*time.Second+time.Millisecond))
				g.publish(t)
				g.consume(t)
			} else {
				g.publish(t)
				before := g.count(t, "SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events")
				if e := proxy.Arm(mode); e != nil {
					t.Fatal(e)
				}
				if worked, e := g.s.ConsumeNext(g.ctx); e == nil || worked {
					t.Fatal("lost/cut ACK returned success", worked, e)
				}
				if g.count(t, "SELECT count(*) FROM task_event_receipts", "SELECT count(*) FROM task_event_receipts") != 1 {
					t.Fatal("ACK loss preceded durable receipt")
				}
				if e := g.s.Transport.Reconnect(g.ctx); e != nil {
					t.Fatal(e)
				}
				if mode == "ack_send_cut" {
					waitEventTime(t, g.ctx, time.Now().Add(time.Duration(g.s.Configuration.AckWaitSeconds)*time.Second+time.Millisecond))
					g.consume(t)
				} else if worked, e := g.s.ConsumeNext(g.ctx); e != nil || worked {
					t.Fatal("confirmed upstream ACK unexpectedly redelivered", worked, e)
				}
				if g.count(t, "SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events") != before+1 {
					t.Fatal("actual ACK recovery repeated logical receipt audit")
				}
			}
			observed := proxy.Observation()
			if observed.Invalid || observed.Dropped != 1 || observed.PublishForwarded != 1 || g.count(t, "SELECT count(*) FROM task_event_receipts", "SELECT count(*) FROM task_event_receipts") != 1 {
				t.Fatal("actual wire boundary evidence incomplete", observed)
			}
			if mode == "publish_send_cut" && observed.PublishAttempts != 2 || mode != "publish_send_cut" && observed.PublishAttempts != 1 {
				t.Fatal("publication replay did not match real readback", observed)
			}
			if mode == "ack_send_cut" && (observed.AckAttempts != 2 || observed.AckForwarded != 1) || mode == "ack_confirmation_lost" && (observed.AckAttempts != 1 || observed.AckForwarded != 1) {
				t.Fatal("ACK wire observations do not match recovery", observed)
			}
			stats, e := g.s.Transport.Inspect(g.ctx)
			if e != nil || stats.AckPending != 0 {
				t.Fatal("broker ACK state did not converge", e)
			}
			record("observation", &observed)
		})
	}
}
