package config

// Explicit private policy input; defaults and validation stay with HTTPPolicy.
const (
	httpInstallationLeaseIntervalEnvironment = "PROJECT_ONE_HTTP_INSTALLATION_LEASE_INTERVAL_SECONDS"
	httpStartupEnvironment                   = "PROJECT_ONE_HTTP_STARTUP_SECONDS"
	httpReadHeaderEnvironment                = "PROJECT_ONE_HTTP_READ_HEADER_SECONDS"
	httpRequestEnvironment                   = "PROJECT_ONE_HTTP_REQUEST_SECONDS"
	httpIdleEnvironment                      = "PROJECT_ONE_HTTP_IDLE_SECONDS"
	httpShutdownEnvironment                  = "PROJECT_ONE_HTTP_SHUTDOWN_SECONDS"
	httpMaxConnectionsEnvironment            = "PROJECT_ONE_HTTP_MAX_CONNECTIONS"
	httpMaxActiveRequestsEnvironment         = "PROJECT_ONE_HTTP_MAX_ACTIVE_REQUESTS"
	httpRateLimitFallbackRetryEnvironment    = "PROJECT_ONE_HTTP_RATE_LIMIT_FALLBACK_RETRY_SECONDS"
	httpServiceUnavailableRetryEnvironment   = "PROJECT_ONE_HTTP_SERVICE_UNAVAILABLE_RETRY_SECONDS"
)

func (p *RuntimePolicy) httpEnvironmentField(key string) *int {
	switch key {
	case httpInstallationLeaseIntervalEnvironment:
		return &p.HTTP.InstallationLeaseIntervalSeconds
	case httpStartupEnvironment:
		return &p.HTTP.StartupSeconds
	case httpReadHeaderEnvironment:
		return &p.HTTP.ReadHeaderSeconds
	case httpRequestEnvironment:
		return &p.HTTP.RequestSeconds
	case httpIdleEnvironment:
		return &p.HTTP.IdleSeconds
	case httpShutdownEnvironment:
		return &p.HTTP.ShutdownSeconds
	case httpMaxConnectionsEnvironment:
		return &p.HTTP.MaxConnections
	case httpMaxActiveRequestsEnvironment:
		return &p.HTTP.MaxActiveRequests
	case httpRateLimitFallbackRetryEnvironment:
		return &p.HTTP.RateLimitFallbackRetrySeconds
	case httpServiceUnavailableRetryEnvironment:
		return &p.HTTP.ServiceUnavailableRetrySeconds
	default:
		return nil
	}
}
