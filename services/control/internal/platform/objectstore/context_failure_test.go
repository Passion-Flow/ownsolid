package objectstore

import (
	"context"
	"errors"
	"testing"
)

func TestRemoteCancellationRetainsFixedCodeAndContextIdentity(t *testing.T) {
	for _, item := range []struct {
		cause error
		code  string
	}{{context.Canceled, "OBJECT_CANCELLED"}, {context.DeadlineExceeded, "OBJECT_TIMEOUT"}} {
		e := safe(item.cause)
		if e.Error() != item.code || !errors.Is(e, item.cause) {
			t.Fatal("context identity lost", e)
		}
	}
}
