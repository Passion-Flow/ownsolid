package training

import (
	"context"
	"io"
	"path/filepath"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

func ReadRemovalChallenge(path string) (wire.RemovalChallenge, error) {
	var c wire.RemovalChallenge
	actual, e := filepath.EvalSymlinks(path)
	if e != nil || actual != path || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return c, fail("TRAINING_REMOVAL_CHALLENGE_INVALID")
	}
	file, e := privateRegular(path, wire.MaximumRemovalBytes)
	if e != nil {
		return c, e
	}
	defer file.Close()
	before, e := file.Stat()
	if e != nil {
		return c, fail("TRAINING_REMOVAL_CHALLENGE_INVALID")
	}
	raw, e := io.ReadAll(io.LimitReader(file, wire.MaximumRemovalBytes+1))
	if e != nil {
		return c, e
	}
	after, e := file.Stat()
	if e != nil || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return c, fail("TRAINING_REMOVAL_CHALLENGE_INVALID")
	}
	return wire.DecodeRemovalChallenge(raw, time.Now())
}

// RemoveFenced preserves the journal and private files, requires an exact audited challenge,
// and freshly observes absence even when the journal already contains an earlier stop receipt.
func (m *Manager) RemoveFenced(ctx context.Context, node, job string, challenge wire.RemovalChallenge) (wire.RemovalReceipt, error) {
	var receipt wire.RemovalReceipt
	if challenge.Validate(time.Now()) != nil || challenge.NodeID != node || challenge.Command.Scope.JobID != job {
		return receipt, fail("TRAINING_REMOVAL_CHALLENGE_INVALID")
	}
	j, e := m.Lifecycle.Store.Read()
	if e != nil {
		return receipt, e
	}
	if j.NodeID != node || j.TaskID != challenge.TaskID || j.CommandSHA256 != challenge.CommandSHA256 {
		return receipt, fail("TRAINING_AGENT_OWNERSHIP_CONFLICT")
	}
	digest, e := j.Command.Digest()
	if e != nil || digest != challenge.CommandSHA256 {
		return receipt, fail("TRAINING_AGENT_OWNERSHIP_CONFLICT")
	}
	stop, e := m.Lifecycle.Stop(ctx, &j)
	if e != nil {
		return receipt, e
	}
	id, e := newUUID()
	if e != nil {
		return receipt, e
	}
	receipt = wire.RemovalReceipt{SchemaVersion: 1, ReceiptID: id, Challenge: challenge, ChallengeSHA256: challenge.Digest(), Stop: stop, ObservedAt: time.Now().UTC()}
	if e = receipt.Validate(time.Now()); e != nil {
		return wire.RemovalReceipt{}, e
	}
	return receipt, nil
}
