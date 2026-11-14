package egress

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// maxPacket bounds one read from the device; a TUN device delivers one packet per read.
const maxPacket = 65535

// Recorder reads packets from a device, writes every attempt that would leave the host
// as one JSON line to its log and rejects the packet.
type Recorder struct {
	device io.ReadWriter
	log    io.Writer
	now    func() time.Time
}

// NewRecorder returns a recorder for device that appends attempts to log.
func NewRecorder(device io.ReadWriter, log io.Writer) *Recorder {
	return &Recorder{device: device, log: log, now: time.Now}
}

// Run handles packets until reading the device fails, and returns that error. Closing
// the device is the way to stop it.
func (r *Recorder) Run() error {
	buffer := make([]byte, maxPacket)
	for {
		n, err := r.device.Read(buffer)
		if err != nil {
			return err
		}
		if err = r.handle(buffer[:n]); err != nil {
			return err
		}
	}
}

// handle records and rejects one packet. Malformed packets are dropped: the kernel
// writes only well-formed ones, and the recorder must not stop on garbage.
func (r *Recorder) handle(raw []byte) error {
	p, err := decode(raw)
	if err != nil || !p.leavesLink() {
		return nil
	}
	line, err := json.Marshal(p.attempt(r.now()))
	if err != nil {
		return fmt.Errorf("encode attempt: %w", err)
	}
	if _, err = r.log.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("write attempt log: %w", err)
	}
	if reply := rejection(p); reply != nil {
		if _, err = r.device.Write(reply); err != nil {
			return fmt.Errorf("write rejection: %w", err)
		}
	}
	return nil
}

// ReadLog returns the attempts recorded in the log at path. A missing log is an error:
// it means the recorder never ran, so nothing was watched.
func ReadLog(path string) ([]Attempt, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open egress log: %w", err)
	}
	defer file.Close()
	var attempts []Attempt
	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		var a Attempt
		if err = json.Unmarshal(scanner.Bytes(), &a); err != nil {
			return nil, fmt.Errorf("egress log line %d: %w", line, err)
		}
		attempts = append(attempts, a)
	}
	if err = scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read egress log: %w", err)
	}
	return attempts, nil
}
