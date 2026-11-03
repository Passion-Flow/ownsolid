package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"github.com/Passion-Flow/ownsolid/services/agent/internal/state"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/training"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

// RemoveFencedTraining performs no network request and never replaces retained node identity.
func RemoveFencedTraining(ctx context.Context, c Config, node, job, challengeFile, receiptFile string) error {
	if profile(c) != wire.DockerAIProfile || !idPattern.MatchString(node) || !idPattern.MatchString(job) || !filepath.IsAbs(receiptFile) || filepath.Clean(receiptFile) != receiptFile {
		return errors.New("AGENT_ARGUMENT_INVALID")
	}
	if e := state.Directory(filepath.Dir(receiptFile)); e != nil {
		return e
	}
	lock, e := state.Lock(c.StateDirectory)
	if e != nil {
		return e
	}
	defer lock.Close()
	identity, e := state.Load(c.StateDirectory)
	if e != nil {
		return e
	}
	if identity.NodeID != node || identity.ExecutionProfile != profile(c) || identity.ProtocolVersion != wire.AIVersion || identity.ControlOrigin != c.ControlOrigin {
		return errors.New("IDENTITY_INVALID")
	}
	challenge, e := training.ReadRemovalChallenge(challengeFile)
	if e != nil {
		return e
	}
	m, e := training.New(c.Training, c.StateDirectory, c.Policy())
	if e != nil {
		return e
	}
	defer m.Close()
	output, e := os.OpenFile(receiptFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return errors.New("RECOVERY_RECEIPT_PATH_INVALID")
	}
	defer output.Close()
	receipt, e := m.RemoveFenced(ctx, node, job, challenge)
	if e != nil {
		return e
	}
	if json.NewEncoder(output).Encode(receipt) != nil || output.Sync() != nil {
		return errors.New("RECOVERY_RECEIPT_WRITE_FAILED")
	}
	parent, e := os.Open(filepath.Dir(receiptFile))
	if e != nil {
		return errors.New("RECOVERY_RECEIPT_WRITE_FAILED")
	}
	defer parent.Close()
	if parent.Sync() != nil {
		return errors.New("RECOVERY_RECEIPT_WRITE_FAILED")
	}
	return nil
}
