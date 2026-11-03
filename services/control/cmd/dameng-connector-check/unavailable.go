//go:build !cgo || !odbc_manager

package main

import "github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"

func checkNative(dameng.ConnectionConfiguration, bool) result {
	return result{FormatVersion: 1, Code: "DAMENG_CHECK_NATIVE_BUILD_REQUIRED", Checks: []string{}, CleanupState: "not_created"}
}
