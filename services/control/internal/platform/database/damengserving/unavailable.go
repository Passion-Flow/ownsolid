//go:build !cgo || !odbc_manager

package damengserving

import (
	"context"
	"errors"
)

// A build without the actual manager cannot select native serving.
func OpenProtected(context.Context, string, string, int) (Runtime, error) {
	return nil, errors.New("DAMENG_NATIVE_SERVING_BUILD_REQUIRED")
}
