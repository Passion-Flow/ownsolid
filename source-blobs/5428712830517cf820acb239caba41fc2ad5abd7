package datasets

import (
	"crypto/sha256"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

const nearFixturePrompt = "Before scheduling any planned maintenance, verify the configured source and store the new temporary reference in the independent workspace archive."

func nearFixtureRows() []wire.Record {
	base := nearFixturePrompt
	typo := strings.Replace(base, "configured", "configurad", 1)
	drift := strings.Replace(typo, "temporary", "temporery", 1)
	return []wire.Record{
		{SourceID: "a", Prompt: base, Completion: "ready"},
		{SourceID: "b", Prompt: typo, Completion: "ready"},
		{SourceID: "c", Prompt: typo, Completion: "ready"},
		{SourceID: "d", Prompt: base, Completion: "denied"},
		{SourceID: "e", Prompt: drift, Completion: "ready"},
		{SourceID: "f", Prompt: "yes", Completion: "ready"},
		{SourceID: "g", Prompt: "yet", Completion: "ready"},
	}
}

func addNearTestRows(t *testing.T, planner *nearPlanner, rows []wire.Record) {
	t.Helper()
	for _, row := range rows {
		key := fmt.Sprintf("%x", sha256.Sum256([]byte(row.Prompt+"\x00"+row.Completion)))
		if err := planner.add(t.Context(), row, key); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNearPromptRetainsDistinctRowsAndJoinsLeakageEdges(t *testing.T) {
	rows := nearFixtureRows()
	negative := strings.Replace(nearFixturePrompt, "verify", "not verify", 1)
	one := strings.Replace(nearFixturePrompt, "archive.", "archive 1.", 1)
	two := strings.Replace(nearFixturePrompt, "archive.", "archive 2.", 1)
	chinese := "\u8bf7\u5148\u786e\u8ba4\u4efb\u52a1\u6570\u636e\u7684\u7248\u672c\u7f16\u53f7\uff0c\u7136\u540e\u6838\u5bf9\u6a21\u578b\u914d\u7f6e\u3001\u8bad\u7ec3\u53c2\u6570\u4ee5\u53ca\u65e5\u5fd7\u4e2d\u7684\u6821\u9a8c\u6458\u8981\uff0c\u6700\u540e\u5c06\u7ed3\u679c\u5199\u5165\u5de5\u4f5c\u533a\u7684\u72ec\u7acb\u5ba1\u8ba1\u8bb0\u5f55\u3002" +
		"\u5e76\u5728\u5b8c\u6210\u64cd\u4f5c\u524d\u6838\u5b9e\u5f53\u524d\u7248\u672c\u4e0e\u5148\u524d\u5feb\u7167\u4e4b\u95f4\u7684\u5dee\u5f02\uff0c\u907f\u514d\u628a\u672a\u9a8c\u8bc1\u7684\u6587\u4ef6\u5199\u5165\u516c\u5f00\u76ee\u5f55\u3002" +
		"\u64cd\u4f5c\u7ed3\u675f\u540e\u4fdd\u5b58\u8bb0\u5f55\u3002"
	for _, prompt := range []string{negative, one, two, chinese, strings.Replace(chinese, "\u6821\u9a8c", "\u6821\u9a57", 1), nearFixturePrompt + " " + nearFixturePrompt} {
		rows = append(rows, wire.Record{SourceID: fmt.Sprint(len(rows)), Prompt: prompt, Completion: "ready"})
	}
	expected := []nearPlanRow{{1, 0}, {1, 1}, {1, 1}, {1, 0}, {1, 0}, {6, 0}, {7, 0}, {1, 0}, {1, 0}, {1, 0}, {11, 0}, {11, 11}, {1, 0}}
	for range 8 {
		planner := newNearPlanner(512 << 20)
		addNearTestRows(t, planner, rows)
		for i, want := range expected {
			if got := (nearPlanRow{planner.root(i + 1), planner.plan[i].Match}); got != want {
				t.Fatalf("line %d: got %+v, want %+v", i+1, got, want)
			}
		}
	}
}

func TestNearPromptIndexBoundsAndOrderIndependentGroups(t *testing.T) {
	random := rand.New(rand.NewPCG(104, 17))
	text := func(n int) string {
		result := make([]byte, n)
		for i := range result {
			result[i] = byte('a' + random.IntN(26))
		}
		return string(result)
	}
	planner := newNearPlanner(512 << 20)
	for i := range 10_000 {
		row := wire.Record{SourceID: fmt.Sprint(i), Prompt: text(74), Completion: "ready"}
		if err := planner.add(t.Context(), row, row.SourceID); err != nil {
			t.Fatal(err)
		}
	}
	if planner.units >= planner.unitLimit || planner.visits > 100_000 {
		t.Fatalf("sparse data exceeded its expected index or candidate work: %d units, %d visits", planner.units, planner.visits)
	}
	indexBound := newNearPlanner(64 << 20)
	indexBound.unitLimit = 10
	dense := newNearPlanner(64 << 20)
	dense.visitLimit = 40
	exactWork := newNearPlanner(64 << 20)
	exactWork.compareLimit = 10
	for _, limited := range []*nearPlanner{indexBound, dense, exactWork} {
		var err error
		for i := range 60 {
			row := wire.Record{SourceID: fmt.Sprint(i), Prompt: nearFixturePrompt + fmt.Sprint(i), Completion: "ready"}
			if err = limited.add(t.Context(), row, row.SourceID); err != nil {
				break
			}
		}
		if err == nil || err.Error() != nearBudgetError {
			t.Fatalf("expected an explicit budget refusal, got %v", err)
		}
	}

	rows := nearFixtureRows()
	for i := range 28 {
		prompt := text(118)
		for _, suffix := range []string{"abc", "axy", "test", "unrelated"} {
			rows = append(rows, wire.Record{SourceID: fmt.Sprintf("oracle-%d-%s", i, suffix), Prompt: prompt + suffix, Completion: suffix})
		}
	}
	assertExhaustive := func() {
		t.Helper()
		index := newNearPlanner(512 << 20)
		addNearTestRows(t, index, rows)
		brute := sourceUnion{}
		sets := make([]map[promptShingle]bool, len(rows))
		for i, row := range rows {
			set := make(map[promptShingle]bool)
			for _, gram := range promptShingles(row.Prompt) {
				set[gram] = true
			}
			sets[i] = set
			for j, previous := range sets[:i] {
				common := 0
				for gram := range set {
					if previous[gram] {
						common++
					}
				}
				union := len(set) + len(previous) - common
				if row.Prompt == rows[j].Prompt || min(len(set), len(previous)) >= nearMinimumShingles && common*10 >= union*9 {
					brute.union(row.SourceID, rows[j].SourceID)
				}
			}
		}
		for i := range rows {
			for j := range i {
				if (index.root(i+1) == index.root(j+1)) != (brute.find(rows[i].SourceID) == brute.find(rows[j].SourceID)) {
					t.Fatalf("indexed groups disagree with exhaustive groups for lines %d and %d", i+1, j+1)
				}
			}
		}
	}
	assertExhaustive()
	random.Shuffle(len(rows), func(i, j int) { rows[i], rows[j] = rows[j], rows[i] })
	assertExhaustive()
}
