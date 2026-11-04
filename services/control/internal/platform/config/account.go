package config

import (
	"errors"
	"path/filepath"
)

// validateComponentReferences checks that optional component file references,
// when present, are canonical absolute paths.
func (c Config) validateComponentReferences() error {
	if c.Components.NodeExporterArchiveFile != "" && !canonicalAbsolutePath(c.Components.NodeExporterArchiveFile) {
		return errors.New("CONFIG_COMPONENT_ARCHIVE_REFERENCE_INVALID")
	}
	if c.Components.NodeExporterCatalogDirectory != "" && !canonicalAbsolutePath(c.Components.NodeExporterCatalogDirectory) {
		return errors.New("CONFIG_COMPONENT_CATALOG_REFERENCE_INVALID")
	}
	return nil
}

func canonicalAbsolutePath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path
}

// ValidateServing checks the rules that must hold before accepting traffic.
// Stores stay optional here: a fresh installation has to serve before first-run
// setup, and BOOTSTRAP_REQUIRED_FOR_CONFIGURED_FEATURES refuses configured stores
// on an unbootstrapped database.
func (c Config) ValidateServing() error {
	if c.Server.EffectiveTransportMode() == TransportHTTP && c.MachineServer == nil {
		return errors.New("CONFIG_NODES_MACHINE_HTTPS_REQUIRED")
	}
	policy := c.Policy()
	if policy.HTTP.RequestSeconds <= policy.Portal.CommitSeconds || policy.HTTP.StartupSeconds <= policy.Portal.CommitSeconds || policy.HTTP.ShutdownSeconds <= policy.Portal.CommitSeconds {
		return errors.New("CONFIG_PORTAL_HTTP_BUDGET_INVALID")
	}
	return nil
}
