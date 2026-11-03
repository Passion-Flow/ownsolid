package main

import "errors"

// scopeBinding is an explicit per-invocation assertion, never deployment input.
// Tracking presence prevents an explicit empty value from removing the fence.
type scopeBinding struct {
	value string
	set   bool
}

func (b *scopeBinding) String() string { return b.value }
func (b *scopeBinding) Set(value string) error {
	if b.set || len(value) != 32 {
		return errors.New("OFFLINE_OPERATOR_SCOPE_BINDING_INVALID")
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'f' || c >= '0' && c <= '9') {
			return errors.New("OFFLINE_OPERATOR_SCOPE_BINDING_INVALID")
		}
	}
	b.value, b.set = value, true
	return nil
}

func (b scopeBinding) validateCommand(command string) error {
	if !b.set {
		return nil
	}
	switch command {
	case "upgrade-cumulative", "reconcile-cumulative-upgrade", "recover-cumulative-upgrade", "inspect-cumulative-upgrade", "activate-cumulative-upgrade", "reconcile-release-view", "status", "inspect-restored-tls", "prepare-restored-tls", "rotate-restored-tls", "reconcile-restored-tls", "abort-restored-tls", "replace-restored-control", "reconcile-restored-control", "recover-restored-control":
		return nil
	default:
		return errors.New("OFFLINE_OPERATOR_SCOPE_BINDING_COMMAND_INVALID")
	}
}
