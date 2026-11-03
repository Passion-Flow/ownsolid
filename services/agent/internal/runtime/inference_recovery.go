package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/Passion-Flow/ownsolid/services/agent/internal/inference"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/state"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

// RemoveFencedInference requires a stopped resident agent and preserves its revoked identity/history.
func RemoveFencedInference(ctx context.Context, c Config, fencedNode, deployment, receiptFile string) error {
	if !wire.IsInferenceProfile(profile(c)) || !idPattern.MatchString(deployment) || !idPattern.MatchString(fencedNode) || !filepath.IsAbs(receiptFile) || filepath.Clean(receiptFile) != receiptFile {
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
	if identity.NodeID != fencedNode || identity.ExecutionProfile != profile(c) || identity.ProtocolVersion != wire.ProfileVersion(profile(c)) || identity.ControlOrigin != c.ControlOrigin {
		return errors.New("IDENTITY_INVALID")
	}
	m, e := inference.New(c.Inference, c.StateDirectory, c.Policy())
	if e != nil {
		return e
	}
	defer m.Close()
	// Reserve the explicit output before effects, never overwrite an operator file.
	output, e := os.OpenFile(receiptFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return errors.New("RECOVERY_RECEIPT_PATH_INVALID")
	}
	defer output.Close()
	receipt, e := m.RemoveFenced(ctx, fencedNode, deployment)
	if e != nil {
		return e
	}
	if json.NewEncoder(output).Encode(receipt) != nil || output.Sync() != nil {
		return errors.New("RECOVERY_RECEIPT_WRITE_FAILED")
	}
	directory, e := os.Open(filepath.Dir(receiptFile))
	if e != nil {
		return errors.New("RECOVERY_RECEIPT_WRITE_FAILED")
	}
	defer directory.Close()
	if directory.Sync() != nil {
		return errors.New("RECOVERY_RECEIPT_WRITE_FAILED")
	}
	return nil
}
