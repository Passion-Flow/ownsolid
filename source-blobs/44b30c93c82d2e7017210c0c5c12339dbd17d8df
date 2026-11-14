package releases

import (
	"errors"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
)

// The contract owns the one closed policy shape; this owner retains protected
// file I/O. No deployment setting can replace production EUID observations.
type Config = view.Configuration

func Load(name string) (Config, error) {
	raw, err := bundle.ReadProtectedConfiguration(name)
	if err != nil {
		return Config{}, errors.New("RELEASE_VIEW_CONFIGURATION_INVALID")
	}
	return view.ParseConfiguration(raw)
}
