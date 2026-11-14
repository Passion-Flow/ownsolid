package training

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

func removalChallenge(j Journal) wire.RemovalChallenge {
	now := time.Now().UTC()
	return wire.RemovalChallenge{SchemaVersion: 1, ChallengeID: fixtureID, NodeID: j.NodeID, TaskID: j.TaskID, Command: j.Command, CommandSHA256: j.CommandSHA256, IssuedAt: now, ExpiresAt: now.Add(wire.RemovalChallengeLifetime)}
}
func TestFencedTrainingRemovalRequiresExactPrivateJournalAndFreshAbsence(t *testing.T) {
	l, j, engine := fixtureLifecycle(t)
	ctx := context.Background()
	if e := l.Start(ctx, j); e != nil {
		t.Fatal(e)
	}
	m := &Manager{Config: l.Config, Lifecycle: *l}
	c := removalChallenge(*j)
	foreign := c
	foreign.TaskID = "00000000-0000-4000-8000-000000000099"
	if _, e := m.RemoveFenced(ctx, j.NodeID, j.Command.Scope.JobID, foreign); e == nil || !engine.container.State.Running {
		t.Fatal("foreign challenge changed owner", e)
	}
	r, e := m.RemoveFenced(ctx, j.NodeID, j.Command.Scope.JobID, c)
	if e != nil || r.Validate(time.Now()) != nil || engine.container != nil {
		t.Fatal("exact synthetic owner removal", e)
	}
	before, e := l.Store.Read()
	if e != nil {
		t.Fatal(e)
	}
	if before.Phase != "removed" || before.StopReceipt == nil {
		t.Fatal("private stop journal lost")
	}
	next := removalChallenge(before)
	replay, e := m.RemoveFenced(ctx, j.NodeID, j.Command.Scope.JobID, next)
	if e != nil || !reflect.DeepEqual(replay.Stop, r.Stop) || replay.ObservedAt.Before(next.IssuedAt) {
		t.Fatal("fresh absence observation", e)
	}
	// A newly appeared object under the same immutable name must prevent an absent receipt.
	expected, image, e := l.expected(ctx, before)
	if e != nil {
		t.Fatal(e)
	}
	value := containerFrom(expected, image, wire.ContainerName(j.Command.AttemptID))
	engine.container = &value
	if _, e = m.RemoveFenced(ctx, j.NodeID, j.Command.Scope.JobID, next); e == nil {
		t.Fatal("prior receipt bypassed current absence check")
	}
}
func TestFencedTrainingChallengeFileRefusesLinksPermissionsAndCorruptBytes(t *testing.T) {
	l, j, _ := fixtureLifecycle(t)
	c := removalChallenge(*j)
	raw, _ := json.Marshal(c)
	file := filepath.Join(l.Store.Directory, "challenge.json")
	if e := os.WriteFile(file, raw, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := ReadRemovalChallenge(file); e != nil {
		t.Fatal(e)
	}
	link := filepath.Join(l.Store.Directory, "linked.json")
	if e := os.Link(file, link); e != nil {
		t.Fatal(e)
	}
	if _, e := ReadRemovalChallenge(file); e == nil {
		t.Fatal("hardlinked challenge accepted")
	}
	os.Remove(link)
	if e := os.Chmod(file, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e := ReadRemovalChallenge(file); e == nil {
		t.Fatal("public challenge accepted")
	}
	os.Chmod(file, 0600)
	if e := os.WriteFile(file, []byte(`{"schema_version":1}`), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := ReadRemovalChallenge(file); e == nil {
		t.Fatal("incomplete challenge accepted")
	}
}
