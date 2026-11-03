package training

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Passion-Flow/ownsolid/services/agent/internal/state"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

const MaximumJournalBytes = 65536

type Journal struct {
	SchemaVersion     int               `json:"schema_version"`
	JournalID         string            `json:"journal_id"`
	NodeID            string            `json:"node_id"`
	TaskID            string            `json:"task_id"`
	LeaseToken        string            `json:"lease_token"`
	Command           wire.Command      `json:"command"`
	CommandSHA256     string            `json:"command_sha256"`
	Phase             string            `json:"phase"`
	ContainerID       string            `json:"container_id"`
	ContainerVerified bool              `json:"container_verified"`
	LastEventSequence int64             `json:"last_event_sequence"`
	OutcomeSHA256     *string           `json:"outcome_sha256"`
	AgentErrorCode    string            `json:"agent_error_code"`
	StopReceipt       *wire.StopReceipt `json:"stop_receipt"`
	CreatedAt         time.Time         `json:"created_at"`
}
type JournalStore struct{ Directory string }

func (s JournalStore) Path() string { return filepath.Join(s.Directory, "training-journal.json") }
func newUUID() (string, error) {
	var value [16]byte
	if _, e := rand.Read(value[:]); e != nil {
		return "", fail("TRAINING_AGENT_IO_FAILED")
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	b := hex.EncodeToString(value[:])
	return b[:8] + "-" + b[8:12] + "-" + b[12:16] + "-" + b[16:20] + "-" + b[20:], nil
}
func validToken(value string) bool {
	if len(value) != 43 {
		return false
	}
	for _, c := range value {
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			continue
		}
		return false
	}
	return true
}
func (j Journal) Validate() error {
	if !wire.ValidAgentError(j.AgentErrorCode) || j.StopReceipt != nil && j.StopReceipt.AgentErrorCode != j.AgentErrorCode {
		return fail("TRAINING_AGENT_JOURNAL_INVALID")
	}
	digest, e := j.Command.Digest()
	if e != nil || j.SchemaVersion != 1 || !datawire.ValidID(j.JournalID) || !datawire.ValidID(j.NodeID) || !datawire.ValidID(j.TaskID) || !validToken(j.LeaseToken) || j.CommandSHA256 != digest || j.ContainerID != "" && !containerID.MatchString(j.ContainerID) || j.ContainerVerified && j.ContainerID == "" || j.LastEventSequence < 0 || j.LastEventSequence > wire.MaximumEventSequence || j.CreatedAt.IsZero() || j.OutcomeSHA256 != nil && !datawire.ValidDigest(*j.OutcomeSHA256) {
		return fail("TRAINING_AGENT_JOURNAL_INVALID")
	}
	switch j.Phase {
	case "preparing", "creating", "running", "stopping", "removed", "acknowledged":
	default:
		return fail("TRAINING_AGENT_JOURNAL_INVALID")
	}
	if j.StopReceipt != nil {
		if j.StopReceipt.ValidateFor(j.Command, j.NodeID, j.TaskID) != nil || j.StopReceipt.JournalID != j.JournalID || j.Phase != "removed" && j.Phase != "acknowledged" || j.StopReceipt.OutcomeSHA256 == nil != (j.OutcomeSHA256 == nil) || j.OutcomeSHA256 != nil && *j.OutcomeSHA256 != *j.StopReceipt.OutcomeSHA256 {
			return fail("TRAINING_AGENT_JOURNAL_INVALID")
		}
	}
	if (j.Phase == "removed" || j.Phase == "acknowledged") && j.StopReceipt == nil {
		return fail("TRAINING_AGENT_JOURNAL_INVALID")
	}
	return nil
}
func syncDirectory(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return fail("TRAINING_AGENT_IO_FAILED")
	}
	defer f.Close()
	if f.Sync() != nil {
		return fail("TRAINING_AGENT_IO_FAILED")
	}
	return nil
}
func privateRegular(path string, maximum int64) (*os.File, error) {
	i, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	owner, ok := i.Sys().(*syscall.Stat_t)
	if !i.Mode().IsRegular() || i.Mode().Perm()&0077 != 0 || !ok || owner.Uid != uint32(os.Geteuid()) || owner.Nlink != 1 || i.Size() > maximum {
		return nil, fail("TRAINING_AGENT_JOURNAL_INVALID")
	}
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return nil, fail("TRAINING_AGENT_IO_FAILED")
	}
	opened, e := f.Stat()
	if e != nil || !os.SameFile(i, opened) {
		f.Close()
		return nil, fail("TRAINING_AGENT_JOURNAL_INVALID")
	}
	return f, nil
}
func (s JournalStore) Read() (Journal, error) {
	var out Journal
	if e := state.Directory(s.Directory); e != nil {
		return out, e
	}
	f, e := privateRegular(s.Path(), MaximumJournalBytes)
	if e != nil {
		return out, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, MaximumJournalBytes+1))
	if e != nil || len(b) > MaximumJournalBytes || wire.DecodeStrict(b, &out) != nil {
		return out, fail("TRAINING_AGENT_JOURNAL_INVALID")
	}
	return out, out.Validate()
}
func (s JournalStore) Save(j Journal) error {
	if state.Directory(s.Directory) != nil || j.Validate() != nil {
		return fail("TRAINING_AGENT_JOURNAL_INVALID")
	}
	if f, e := privateRegular(s.Path(), MaximumJournalBytes); e == nil {
		f.Close()
	} else if !os.IsNotExist(e) {
		return e
	}
	b, e := json.Marshal(j)
	if e != nil || len(b) > MaximumJournalBytes {
		return fail("TRAINING_AGENT_JOURNAL_INVALID")
	}
	f, e := os.CreateTemp(s.Directory, ".training-journal-")
	if e != nil {
		return fail("TRAINING_AGENT_IO_FAILED")
	}
	name := f.Name()
	defer os.Remove(name)
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	closeError := f.Close()
	if e != nil || closeError != nil {
		return fail("TRAINING_AGENT_IO_FAILED")
	}
	if os.Rename(name, s.Path()) != nil {
		return fail("TRAINING_AGENT_IO_FAILED")
	}
	return syncDirectory(s.Directory)
}
func (s JournalStore) Begin(node, task, token string, command wire.Command) (Journal, error) {
	var out Journal
	if previous, e := s.Read(); e == nil {
		digest, _ := command.Digest()
		if previous.NodeID == node && previous.TaskID == task && previous.LeaseToken == token && previous.CommandSHA256 == digest {
			return previous, nil
		}
		return out, fail("TRAINING_AGENT_PREVIOUS_ATTEMPT_REQUIRES_RECONCILIATION")
	} else if !os.IsNotExist(e) {
		return out, e
	}
	id, e := newUUID()
	if e != nil {
		return out, e
	}
	digest, e := command.Digest()
	if e != nil {
		return out, e
	}
	out = Journal{SchemaVersion: 1, JournalID: id, NodeID: node, TaskID: task, LeaseToken: token, Command: command, CommandSHA256: digest, Phase: "preparing", CreatedAt: time.Now().UTC()}
	return out, s.Save(out)
}
