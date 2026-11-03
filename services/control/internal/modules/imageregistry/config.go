package imageregistry

import (
	"errors"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/registryconfig"
	"strings"
)

// Config aliases the single platform boundary used by deployment and business owners.
type Config = registryconfig.Config

func Defaults() Config                     { return registryconfig.Defaults() }
func Load(filename string) (Config, error) { return registryconfig.Load(filename) }
func LoadEnvironment(values map[string]string) (Config, error) {
	return registryconfig.LoadEnvironment(values)
}
func failure(code string) error { return errors.New(code) }

func withinPath(root, name string) bool {
	value, err := filepath.Rel(root, name)
	return err == nil && value != ".." && !strings.HasPrefix(value, ".."+string(filepath.Separator))
}
