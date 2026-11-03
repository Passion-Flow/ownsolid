package v1

import (
	"bytes"
	"errors"
)

// RegistryDeliveryCapability reports a constructed handler, not image readiness.
// Reject null as well as truthy strings/numbers at the wire boundary.
type RegistryDeliveryCapability bool

func (value *RegistryDeliveryCapability) UnmarshalJSON(raw []byte) error {
	switch string(bytes.TrimSpace(raw)) {
	case "true":
		*value = true
	case "false":
		*value = false
	default:
		return errors.New("VALIDATION_FAILED")
	}
	return nil
}
