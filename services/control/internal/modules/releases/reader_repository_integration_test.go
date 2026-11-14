//go:build repository_integration

package releases

import (
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/localization"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
)

// NewSnapshotReaderTestService exists only in the tagged test archive. It lets
// the external HTTP test import the real Identity dispatcher without an import
// cycle. No production constructor, config field or runtime switch accepts a
// reader. This double does not qualify protected-file or publisher UID checks.
func NewSnapshotReaderTestService(i *identity.Service, l *localization.Registry, c Config, reader func(Config) (view.Snapshot, error)) (*Service, error) {
	if reader == nil {
		return nil, errors.New("RELEASE_VIEW_TEST_READER_REQUIRED")
	}
	s, err := New(i, l, c)
	if err != nil {
		return nil, err
	}
	s.reader = reader
	return s, nil
}
