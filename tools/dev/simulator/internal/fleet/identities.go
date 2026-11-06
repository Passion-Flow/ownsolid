package fleet

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/tools/dev/simulator/internal/link"
)

// identitiesFormat is the version of the identities file.
const identitiesFormat = 1

// maximumIdentitiesBytes bounds the identities file: about 300 bytes per agent.
const maximumIdentitiesBytes = 64 << 20

var (
	nodeID     = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	credential = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
)

// Identity is one enrolled simulated node and its task cursor.
type Identity struct {
	Hostname             string `json:"hostname"`
	NodeID               string `json:"node_id"`
	SiteID               string `json:"site_id"`
	CellID               string `json:"cell_id"`
	Credential           string `json:"credential"`
	AcknowledgedSequence uint64 `json:"acknowledged_sequence"`
}

func (i Identity) node() link.Node {
	return link.Node{ID: i.NodeID, Scope: wire.Scope{SiteID: i.SiteID, CellID: i.CellID}, Credential: i.Credential}
}

func (i Identity) valid() bool {
	return i.Hostname != "" && len(i.Hostname) <= 255 && nodeID.MatchString(i.NodeID) && credential.MatchString(i.Credential) &&
		(wire.Scope{SiteID: i.SiteID, CellID: i.CellID}).Valid() && i.AcknowledgedSequence <= wire.MaxTaskSequence
}

// identities is the file enroll writes and run reads and updates.
type identities struct {
	FormatVersion int        `json:"format_version"`
	ControlOrigin string     `json:"control_origin"`
	Agents        []Identity `json:"agents"`
}

// loadIdentities reads the identities enrolled with origin.
func loadIdentities(path, origin string) ([]Identity, error) {
	invalid := errors.New("SIMULATOR_IDENTITIES_INVALID")
	f, e := os.Open(path)
	if e != nil {
		return nil, errors.New("SIMULATOR_IDENTITIES_UNREADABLE")
	}
	defer f.Close()
	if info, e := f.Stat(); e != nil || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("SIMULATOR_IDENTITIES_EXPOSED")
	}
	raw, e := io.ReadAll(io.LimitReader(f, maximumIdentitiesBytes+1))
	if e != nil || len(raw) > maximumIdentitiesBytes {
		return nil, invalid
	}
	var file identities
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&file) != nil || decoder.Decode(new(any)) != io.EOF || file.FormatVersion != identitiesFormat || file.ControlOrigin != origin {
		return nil, invalid
	}
	seen := map[string]bool{}
	for _, identity := range file.Agents {
		if !identity.valid() || seen[identity.NodeID] {
			return nil, invalid
		}
		seen[identity.NodeID] = true
	}
	return file.Agents, nil
}

// saveIdentities replaces the identities file.
func saveIdentities(path, origin string, agents []Identity) error {
	raw, e := json.MarshalIndent(identities{FormatVersion: identitiesFormat, ControlOrigin: origin, Agents: agents}, "", "  ")
	if e != nil {
		return e
	}
	return writeFile(path, append(raw, '\n'))
}

// writeFile replaces path with content, readable by its owner only. A reader never sees
// a partial file.
func writeFile(path string, content []byte) error {
	temporary, e := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if e != nil {
		return e
	}
	name := temporary.Name()
	_, e = temporary.Write(content)
	if e == nil {
		e = temporary.Chmod(0o600)
	}
	if e == nil {
		e = temporary.Sync()
	}
	if closed := temporary.Close(); e == nil {
		e = closed
	}
	if e == nil {
		e = os.Rename(name, path)
	}
	if e != nil {
		os.Remove(name)
	}
	return e
}
