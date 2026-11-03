//go:build !linux

package components

import "errors"

func diskAvailable(string) (uint64, error) { return 0, errors.New("COMPONENT_PROFILE_UNAVAILABLE") }
