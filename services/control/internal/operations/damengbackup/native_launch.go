//go:build cgo && odbc_manager

package damengbackup

import (
	"context"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
)

// NewNativeContext uses the native owner's verified loader profile. It never
// changes parent cwd/environment. Relative-RPATH tools run only in the declared
// private bin layout; connection/output paths remain explicit absolute inputs.
func NewNativeContext(ctx context.Context, c Configuration, connector dameng.ConnectionConfiguration) (*Native, error) {
	settings, e := dameng.LaunchSettings(connector)
	if e != nil || settings.FormatVersion != 1 {
		return nil, fail("NATIVE_LAUNCH_SETTINGS_REQUIRED")
	}
	if settings.WorkingDirectory == "" {
		return newNative(ctx, c, "", nil)
	}
	return newNative(ctx, c, settings.WorkingDirectory, settings.Environment)
}

// WithConfiguredNativeCustody is the central operator integration hook: one
// validated recovery configuration, one protected native-context document and
// an already admitted matching kernel. It constructs no replacement pool.
func WithConfiguredNativeCustody(ctx context.Context, recovery Configuration, kernel *dameng.Store, configuration NativeContextConfiguration, mode MaintenanceMode, operation func(context.Context, *Native, *NativeCustody) error) error {
	if configuration.Validate() != nil || operation == nil {
		return fail("NATIVE_CONTEXT_INVALID")
	}
	connector, e := dameng.LoadConnectionConfiguration(configuration.ConnectorConfigurationFile, "")
	if e != nil {
		return fail("NATIVE_CONNECTOR_CONFIGURATION_INVALID")
	}
	schema, e := dameng.LoadSchemaConfiguration(configuration.SchemaConfigurationFile, "")
	if e != nil {
		return fail("NATIVE_SCHEMA_CONFIGURATION_INVALID")
	}
	n, e := NewNativeContext(ctx, recovery, connector)
	if e != nil {
		return e
	}
	return WithNativeCustody(ctx, n, kernel, connector, schema, configuration.Snapshot, mode, func(ctx context.Context, c *NativeCustody) error { return operation(ctx, n, c) })
}
