package scope

import (
	"context"
	"strings"
	"testing"
)

func TestControlReplacementRecoveryConfirmsNonapplicationBeforeFreshExplicitIntent(t *testing.T) {
	for _, fault := range []string{"inspector-create-before", "inspector-remove-before", "remove-before", "create-before"} {
		t.Run(fault, func(t *testing.T) {
			c, _, d, w := replacementControllerFixture(t)
			d.fault = fault
			if _, e := replaceRestoredControl(context.Background(), c, d, w, false); e == nil {
				t.Fatal("missing effect accepted")
			}
			removed, created, inspectors := d.removed, w.creates, d.utilityCreates
			out, e := runControlReplacement(context.Background(), c, d, w, false, true)
			if e != nil || out.Status != "control_replacement_confirmed_not_applied_recovery_required" || !out.Pending || out.ProofSHA256 != "" || d.removed != removed || w.creates != created || d.utilityCreates != inspectors {
				t.Fatal("read-only confirmation", out, e)
			}
			for _, reconcile := range []bool{false, true} {
				if _, e := replaceRestoredControl(context.Background(), c, d, w, reconcile); e == nil || d.removed != removed || w.creates != created || d.utilityCreates != inspectors {
					t.Fatal("ordinary retry bypassed explicit recovery")
				}
			}
			out, e = runControlReplacement(context.Background(), c, d, w, false, true)
			if e != nil || out.Pending || out.ProofSHA256 == "" || d.removed != 1 || w.creates != 1 || d.utilityCreates != 2 {
				t.Fatal("fresh retry", out, e)
			}
			l, e := open(c, false)
			if e != nil {
				t.Fatal(e)
			}
			defer l.close()
			confirmations, retries := 0, 0
			for _, event := range l.events {
				if event.Action == "control_replacement_recovery" {
					confirmations++
				}
				if event.Action == "control_replacement_retry_effect" && event.Outcome == "started" {
					retries++
				}
			}
			if confirmations != 1 || retries != 1 {
				t.Fatal("missing distinct durable intent", confirmations, retries)
			}
		})
	}
}
func TestControlReplacementRecoveryLostCompletedEffectCannotClaimNonapplication(t *testing.T) {
	c, _, d, w := replacementControllerFixture(t)
	d.fault = "remove-after"
	if _, e := replaceRestoredControl(context.Background(), c, d, w, false); e == nil {
		t.Fatal("lost effect accepted")
	}
	if _, e := runControlReplacement(context.Background(), c, d, w, false, true); e == nil {
		t.Fatal("completed removal called not applied")
	}
	if out, e := replaceRestoredControl(context.Background(), c, d, w, true); e != nil || out.Pending || d.removed != 1 || w.creates != 1 {
		t.Fatal("ordinary success readback", out, e)
	}
}
func TestControlReplacementRecoveryLostRetryStillRequiresIndependentReconciliation(t *testing.T) {
	c, _, d, w := replacementControllerFixture(t)
	d.fault = "create-before"
	if _, e := replaceRestoredControl(context.Background(), c, d, w, false); e == nil {
		t.Fatal("missing effect accepted")
	}
	if _, e := runControlReplacement(context.Background(), c, d, w, false, true); e != nil {
		t.Fatal(e)
	}
	d.fault = "create-after"
	d.fired = false
	if _, e := runControlReplacement(context.Background(), c, d, w, false, true); e == nil {
		t.Fatal("lost retry accepted")
	}
	if _, e := replaceRestoredControl(context.Background(), c, d, w, false); e == nil || w.creates != 1 {
		t.Fatal("unknown retry resent")
	}
	if out, e := replaceRestoredControl(context.Background(), c, d, w, true); e != nil || out.Pending || w.creates != 1 || d.removed != 1 {
		t.Fatal("retry readback", out, e)
	}
}
func TestControlReplacementRecoveryJournalRejectsStaleOrForgedReferences(t *testing.T) {
	events, base := replacementJournalFixture(t)
	for i := 0; i < 4; i++ {
		events = appendReplacementStep(t, events, base, i)
	}
	intent := replacementIntent(t, base, 4)
	if e := validateImageEvent(events, intent); e != nil {
		t.Fatal(e)
	}
	events = append(events, intent)
	confirm := intent
	confirm.Action = "control_replacement_recovery"
	confirm.OperationID, _ = nonce()
	confirm.ReferenceOperationID = intent.OperationID
	confirm.Outcome = "not_applied"
	for _, fault := range []string{"reference", "operation", "phase", "payload"} {
		t.Run(fault, func(t *testing.T) {
			changed := confirm
			r := *confirm.ControlReplacement
			changed.ControlReplacement = &r
			switch fault {
			case "reference":
				changed.ReferenceOperationID = strings.Repeat("0", 32)
			case "operation":
				changed.OperationID = intent.OperationID
			case "phase":
				r.Step = 2
				r.Phase = "payload-verify"
			case "payload":
				r.ObjectID = strings.Repeat("f", 64)
			}
			if e := validateImageEvent(events, changed); e == nil {
				t.Fatal("forged confirmation")
			}
		})
	}
	if e := validateImageEvent(events, confirm); e != nil {
		t.Fatal(e)
	}
	events = append(events, confirm)
	if e := validateImageEvent(events, replacementIntent(t, base, 4)); e == nil {
		t.Fatal("ordinary intent after nonapplication")
	}
	retry := replacementIntent(t, base, 4)
	retry.Action = "control_replacement_retry_effect"
	retry.ReferenceOperationID = confirm.OperationID
	wrong := retry
	wrong.ReferenceOperationID = intent.OperationID
	if e := validateImageEvent(events, wrong); e == nil {
		t.Fatal("retry referenced uncertain intent instead of confirmed observation")
	}
	if e := validateImageEvent(events, retry); e != nil {
		t.Fatal(e)
	}
	events = append(events, retry)
	done := replacementCompletion(events, retry)
	if e := validateImageEvent(events, done); e != nil {
		t.Fatal("retry terminal reference", e)
	}
}
