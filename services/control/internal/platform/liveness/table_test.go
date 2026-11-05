package liveness

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

var (
	base       = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	thresholds = Thresholds{Online: 30 * time.Second, Offline: 120 * time.Second}
)

func table(t *testing.T, started time.Time) *Table[string] {
	t.Helper()
	out, e := New[string](thresholds, started)
	if e != nil {
		t.Fatal(e)
	}
	return out
}

func TestNewRefusesThresholdsOutOfOrder(t *testing.T) {
	for _, v := range []Thresholds{{}, {Online: time.Second}, {Online: time.Minute, Offline: time.Minute}, {Online: time.Minute, Offline: time.Second}} {
		if _, e := New[string](v, base); e == nil || e.Error() != "LIVENESS_THRESHOLDS_INVALID" {
			t.Errorf("%+v: got %v", v, e)
		}
	}
}

func TestObserveOnlyMovesForwardAndCoalesces(t *testing.T) {
	l := table(t, base)
	l.Observe("n", base.Add(10*time.Second), "new")
	l.Observe("n", base, "old")
	if seen, ok := l.Seen("n"); !ok || !seen.Equal(base.Add(10*time.Second)) {
		t.Fatalf("seen %v %v", seen, ok)
	}
	got := l.Drain(10)
	if len(got) != 1 || got[0].Report != "new" || !got[0].At.Equal(base.Add(10*time.Second)) {
		t.Fatalf("drained %+v", got)
	}
	if again := l.Drain(10); len(again) != 0 {
		t.Fatalf("drained twice: %+v", again)
	}
}

func TestDrainHonorsLimitAndLeavesTheRest(t *testing.T) {
	l := table(t, base)
	for i := range 100 {
		l.Observe(fmt.Sprintf("n%d", i), base, "")
	}
	seen := map[string]bool{}
	for _, size := range []int{30, 30, 30, 30} {
		batch := l.Drain(size)
		if len(batch) > size {
			t.Fatalf("batch of %d over limit %d", len(batch), size)
		}
		for _, v := range batch {
			if seen[v.Node] {
				t.Fatalf("%s drained twice", v.Node)
			}
			seen[v.Node] = true
		}
	}
	if len(seen) != 100 {
		t.Fatalf("drained %d of 100", len(seen))
	}
}

func TestRestoreKeepsTheNewerReportAndTheForgotten(t *testing.T) {
	l := table(t, base)
	l.Observe("n", base, "first")
	l.Observe("gone", base, "first")
	failed := l.Drain(10)
	l.Observe("n", base.Add(time.Second), "second")
	l.Forget("gone")
	l.Restore(failed)
	got := l.Drain(10)
	if len(got) != 1 || got[0].Node != "n" || got[0].Report != "second" || !got[0].At.Equal(base.Add(time.Second)) {
		t.Fatalf("drained %+v", got)
	}

	l.Observe("n", base.Add(2*time.Second), "third")
	failed = l.Drain(10)
	l.Restore(failed)
	if got = l.Drain(10); len(got) != 1 || got[0].Report != "third" {
		t.Fatalf("restored report lost: %+v", got)
	}
}

func TestMergeIsNotDrained(t *testing.T) {
	l := table(t, base)
	l.Merge("n", base.Add(time.Minute))
	l.Merge("n", base)
	if seen, _ := l.Seen("n"); !seen.Equal(base.Add(time.Minute)) {
		t.Fatalf("seen %v", seen)
	}
	if got := l.Drain(10); len(got) != 0 {
		t.Fatalf("drained %+v", got)
	}
}

func TestLoadNeverOverridesWhatWasHeard(t *testing.T) {
	l := table(t, base)
	l.Observe("n", base.Add(time.Minute), "")
	l.Load("n", base, Offline)
	if seen, _ := l.Seen("n"); !seen.Equal(base.Add(time.Minute)) {
		t.Fatalf("seen %v", seen)
	}
}

func TestUnchangedRequiresTheSameRecentState(t *testing.T) {
	l := table(t, base)
	a, b := [32]byte{1}, [32]byte{2}
	if l.Unchanged("n", a, base, time.Minute) {
		t.Fatal("unknown node reported unchanged")
	}
	l.Accept("n", a, base)
	if l.Size() != 0 {
		t.Fatal("accept created a node that was never heard")
	}
	l.Observe("n", base, "")
	if l.Unchanged("n", [32]byte{}, base, time.Minute) {
		t.Fatal("node with no written state reported unchanged")
	}
	l.Accept("n", a, base)
	if !l.Unchanged("n", a, base.Add(time.Minute), time.Minute) {
		t.Fatal("same digest within the age refused")
	}
	if l.Unchanged("n", a, base.Add(time.Minute+time.Nanosecond), time.Minute) {
		t.Fatal("digest past the age accepted")
	}
	if l.Unchanged("n", b, base, time.Minute) {
		t.Fatal("other digest accepted")
	}
}

func TestTransitionsFollowTheThresholds(t *testing.T) {
	l := table(t, base)
	l.Observe("n", base, "")
	for _, c := range []struct {
		age  time.Duration
		want Connectivity
	}{
		{30 * time.Second, ""},
		{30*time.Second + 1, Unknown},
		{120 * time.Second, ""},
		{120*time.Second + 1, Offline},
	} {
		got := l.Transitions(base.Add(c.age), 10)
		if c.want == "" {
			if len(got) != 0 {
				t.Fatalf("age %v: %+v", c.age, got)
			}
			continue
		}
		if len(got) != 1 || got[0].To != c.want || !got[0].LastSeen.Equal(base) {
			t.Fatalf("age %v: %+v", c.age, got)
		}
		l.Recorded(got)
	}
	back := base.Add(time.Hour)
	l.Observe("n", back, "")
	got := l.Transitions(back, 10)
	if len(got) != 1 || got[0].To != Online {
		t.Fatalf("return: %+v", got)
	}
	l.Recorded(got)
	if got = l.Transitions(back, 10); len(got) != 0 {
		t.Fatalf("recorded transition returned again: %+v", got)
	}
}

func TestUnrecordedTransitionIsReturnedAgain(t *testing.T) {
	l := table(t, base)
	l.Observe("n", base, "")
	now := base.Add(time.Hour)
	if len(l.Transitions(now, 10)) != 1 || len(l.Transitions(now, 10)) != 1 {
		t.Fatal("a transition that was not stored must stay pending")
	}
}

func TestRestartDoesNotReadAsAnOutage(t *testing.T) {
	started := base.Add(time.Hour)
	l := table(t, started)
	l.Load("healthy", base, Online)
	l.Load("dead", base, Offline)
	if got := l.Transitions(started.Add(30*time.Second), 10); len(got) != 0 {
		t.Fatalf("inside the grace: %+v", got)
	}
	got := l.Transitions(started.Add(121*time.Second), 10)
	if len(got) != 1 || got[0].Node != "healthy" || got[0].To != Offline {
		t.Fatalf("after the grace: %+v", got)
	}
}

func TestTransitionsHonorLimit(t *testing.T) {
	l := table(t, base)
	for i := range 100 {
		l.Observe(fmt.Sprintf("n%d", i), base, "")
	}
	if got := l.Transitions(base.Add(time.Hour), 7); len(got) != 7 {
		t.Fatalf("got %d", len(got))
	}
}

func TestForgetAndSize(t *testing.T) {
	l := table(t, base)
	l.Observe("a", base, "")
	l.Observe("b", base, "")
	l.Forget("a")
	if _, ok := l.Seen("a"); ok || l.Size() != 1 {
		t.Fatalf("size %d", l.Size())
	}
	if l.Unchanged("a", [32]byte{}, base, time.Minute) {
		t.Fatal("forgotten node reported unchanged")
	}
}

func TestConcurrentUse(t *testing.T) {
	l := table(t, base)
	var wg sync.WaitGroup
	for w := range 8 {
		wg.Go(func() {
			for i := range 2000 {
				node := fmt.Sprintf("n%d", i%50)
				at := base.Add(time.Duration(w*i) * time.Millisecond)
				l.Observe(node, at, "r")
				l.Merge(node, at)
				l.Accept(node, [32]byte{byte(w)}, at)
				l.Unchanged(node, [32]byte{byte(w)}, at, time.Minute)
				if i%100 == 0 {
					l.Restore(l.Drain(10))
					l.Recorded(l.Transitions(at, 10))
					l.Forget(node)
				}
			}
		})
	}
	wg.Wait()
	if l.Size() > 50 {
		t.Fatalf("size %d", l.Size())
	}
}

func BenchmarkObserve(b *testing.B) {
	l, e := New[string](thresholds, base)
	if e != nil {
		b.Fatal(e)
	}
	nodes := make([]string, 10000)
	for i := range nodes {
		nodes[i] = fmt.Sprintf("n%d", i)
	}
	b.ResetTimer()
	for i := range b.N {
		l.Observe(nodes[i%len(nodes)], base.Add(time.Duration(i)), "")
	}
}
