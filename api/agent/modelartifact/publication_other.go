//go:build !linux && !darwin

package modelartifact

import "errors"

func publishDirectory(int, string, int, string) error {
	return errors.New("MODEL_PUBLICATION_PLATFORM_UNSUPPORTED")
}
