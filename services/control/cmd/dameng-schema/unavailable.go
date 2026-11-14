//go:build !cgo || !odbc_manager

package main

import "github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"

func operateNative(string, dameng.ConnectionConfiguration, dameng.SchemaConfiguration, *dameng.ConnectionConfiguration, nativeOperatorOptions) result {
	r := emptyResult()
	r.Code = "DAMENG_SCHEMA_NATIVE_BUILD_REQUIRED"
	return r
}
