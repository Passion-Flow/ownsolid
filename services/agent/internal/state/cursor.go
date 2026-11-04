package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

// cursorFile holds the highest task sequence this agent acknowledged (ADR 0011).
const cursorFile = "task-cursor.json"

type cursor struct {
	AcknowledgedSequence uint64 `json:"acknowledged_sequence"`
}

// LoadCursor returns the agent's task cursor. An agent that never acknowledged a task has
// none, which reads as 0; control answers a cursor it does not expect with a resync.
func LoadCursor(dir string) (uint64, error) {
	if e := Directory(dir); e != nil {
		return 0, e
	}
	path := filepath.Join(dir, cursorFile)
	if _, e := os.Lstat(path); errors.Is(e, os.ErrNotExist) {
		return 0, nil
	}
	b, e := ProtectedFile(path)
	if e != nil {
		return 0, errors.New("TASK_CURSOR_INVALID")
	}
	var value cursor
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&value) != nil || d.Decode(new(any)) != io.EOF || value.AcknowledgedSequence > wire.MaxTaskSequence {
		return 0, errors.New("TASK_CURSOR_INVALID")
	}
	return value.AcknowledgedSequence, nil
}

// SaveCursor durably replaces the task cursor. The agent saves it before it acknowledges
// a lease, so its cursor is never behind what control recorded.
func SaveCursor(dir string, sequence uint64) error {
	if e := Directory(dir); e != nil {
		return e
	}
	if sequence > wire.MaxTaskSequence {
		return errors.New("TASK_CURSOR_INVALID")
	}
	b, _ := json.Marshal(cursor{AcknowledgedSequence: sequence})
	temp, e := os.CreateTemp(dir, ".task-cursor-*")
	if e != nil {
		return errors.New("TASK_CURSOR_WRITE_FAILED")
	}
	name := temp.Name()
	defer os.Remove(name)
	if _, e = temp.Write(b); e == nil {
		e = temp.Sync()
	}
	closeError := temp.Close()
	if e != nil || closeError != nil {
		return errors.New("TASK_CURSOR_WRITE_FAILED")
	}
	if e = os.Rename(name, filepath.Join(dir, cursorFile)); e != nil {
		return errors.New("TASK_CURSOR_WRITE_FAILED")
	}
	d, e := os.Open(dir)
	if e != nil {
		return errors.New("TASK_CURSOR_WRITE_FAILED")
	}
	defer d.Close()
	if d.Sync() != nil {
		return errors.New("TASK_CURSOR_WRITE_FAILED")
	}
	return nil
}
