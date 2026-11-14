// Package taskbus transports immutable task events. It never grants node
// credentials, a physical task lease, execution authority or task-state changes.
package taskbus

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

const EventSchema = 1
const NodeTaskQueued = "node.task.queued.v1"
const MaximumEnvelopeBytes = 4096
const subjectPrefix = "projectone.tasks.v1"

type Event struct {
	Schema        int       `json:"schema"`
	ID            string    `json:"event_id"`
	SiteID        string    `json:"site_id"`
	CellID        string    `json:"cell_id"`
	NamespaceID   string    `json:"namespace_id"`
	TenantID      string    `json:"tenant_id"`
	WorkspaceID   string    `json:"workspace_id"`
	NodeID        string    `json:"node_id"`
	TaskID        string    `json:"task_id"`
	Kind          string    `json:"kind"`
	Operation     string    `json:"operation"`
	TaskCreatedAt time.Time `json:"task_created_at"`
	CreatedAt     time.Time `json:"created_at"`
}

type Envelope struct {
	DeliveryID string `json:"delivery_id"`
	Event      Event  `json:"event"`
}

func ValidID(value string) bool {
	id, e := uuid.Parse(value)
	return e == nil && id != uuid.Nil && id.String() == value
}

// InitialOperation reuses the actual negotiated agent catalog. Unsupported
// families need their owning same-transaction event seam before activation.
func InitialOperation(operation string) bool {
	return operation == wire.CollectFacts || wire.IsComponentOperation(operation)
}

func eventTime(t time.Time) bool {
	return !t.IsZero() && t.Location() == time.UTC && t.Year() >= 1970 && t.Year() <= 9999 && t.Nanosecond()%1000 == 0
}

func (e Event) Validate() error {
	if e.Schema != EventSchema || e.Kind != NodeTaskQueued || !InitialOperation(e.Operation) || !eventTime(e.TaskCreatedAt) || !eventTime(e.CreatedAt) || e.CreatedAt.Before(e.TaskCreatedAt) {
		return errors.New("TASK_EVENT_INVALID")
	}
	if !(wire.Scope{SiteID: e.SiteID, CellID: e.CellID}).Valid() {
		return errors.New("TASK_EVENT_INVALID")
	}
	for _, id := range []string{e.ID, e.NamespaceID, e.TenantID, e.WorkspaceID, e.NodeID, e.TaskID} {
		if !ValidID(id) {
			return errors.New("TASK_EVENT_INVALID")
		}
	}
	return nil
}

func (e Event) Canonical() ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(e)
	if err != nil || len(raw) > MaximumEnvelopeBytes {
		return nil, errors.New("TASK_EVENT_INVALID")
	}
	return raw, nil
}

func (e Event) Digest() (string, error) {
	raw, err := e.Canonical()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func (e Envelope) Canonical() ([]byte, error) {
	if !ValidID(e.DeliveryID) {
		return nil, errors.New("TASK_EVENT_INVALID")
	}
	if err := e.Event.Validate(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(e)
	if err != nil || len(raw) > MaximumEnvelopeBytes {
		return nil, errors.New("TASK_EVENT_INVALID")
	}
	return raw, nil
}

// DecodeEnvelope accepts only the exact canonical bytes emitted by the outbox.
// Re-encoding also rejects duplicate/case-aliased/unknown fields, alternate time
// spelling and extra JSON without creating another generic decoder authority.
func DecodeEnvelope(raw []byte) (Envelope, error) {
	var out Envelope
	if len(raw) == 0 || len(raw) > MaximumEnvelopeBytes || json.Unmarshal(raw, &out) != nil {
		return Envelope{}, errors.New("TASK_EVENT_INVALID")
	}
	canonical, err := out.Canonical()
	if err != nil || !bytes.Equal(raw, canonical) {
		return Envelope{}, errors.New("TASK_EVENT_INVALID")
	}
	return out, nil
}

func FilterSubject(namespace, tenant string) (string, error) {
	if !ValidID(namespace) || !ValidID(tenant) {
		return "", errors.New("TASK_EVENT_INVALID")
	}
	local := wire.LocalScope()
	return strings.Join([]string{subjectPrefix, local.SiteID, local.CellID, namespace, tenant, "*", "*", "queued", "*", "*"}, "."), nil
}

func (e Envelope) Subject() (string, error) {
	if _, err := e.Canonical(); err != nil {
		return "", err
	}
	v := e.Event
	return strings.Join([]string{subjectPrefix, v.SiteID, v.CellID, v.NamespaceID, v.TenantID, v.WorkspaceID, v.NodeID, "queued", v.ID, e.DeliveryID}, "."), nil
}

// MatchesSubject checks structural correlation only. The consumer must also
// match the actual SQL event, task scope, current generation and logical digest.
func (e Envelope) MatchesSubject(subject string) bool {
	expected, err := e.Subject()
	return err == nil && subject == expected
}
