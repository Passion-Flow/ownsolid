package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const (
	supportOperationLogBytes = 64 * 1024
	supportOperationLimit    = 100
)

type supportOperation struct {
	Time    time.Time `json:"time"`
	Action  string    `json:"action"`
	Outcome string    `json:"outcome"`
}

func collectSupportOperations(c Config) ([]supportOperation, error) {
	events := []supportOperation{}
	root, err := os.OpenRoot(c.InstallRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return events, nil
	}
	if err != nil {
		return events, err
	}
	defer root.Close()

	f, err := root.OpenFile(filepath.Join(operatorDirectoryName, operationLogName), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return events, nil
	}
	if err != nil {
		return events, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return events, err
	}
	if !info.Mode().IsRegular() {
		return events, errors.New("operation log is not a regular file")
	}
	start := max(0, info.Size()-supportOperationLogBytes)
	raw := make([]byte, info.Size()-start)
	if len(raw) > 0 {
		if _, err := f.ReadAt(raw, start); err != nil {
			return events, err
		}
	}
	if start > 0 {
		// The first record may begin before the retained tail.
		_, raw, _ = bytes.Cut(raw, []byte("\n"))
	}
	for {
		line, remaining, complete := bytes.Cut(raw, []byte("\n"))
		if !complete {
			break
		}
		raw = remaining
		var event supportOperation
		if json.Unmarshal(line, &event) != nil || event.Time.IsZero() {
			continue
		}
		if (event.Action != "backup" && event.Action != "restore") ||
			(event.Outcome != "complete" && event.Outcome != "failed") {
			continue
		}
		event.Time = event.Time.UTC()
		events = append(events, event)
	}
	return events[max(0, len(events)-supportOperationLimit):], nil
}
