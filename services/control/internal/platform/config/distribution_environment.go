package config

// Protected --env-file mapping; defaults and cross-field bounds remain in DistributionPolicy.
func (p *DistributionPolicy) environmentField(key string) *int {
	switch key {
	case "PROJECT_ONE_DISTRIBUTION_RECOVERY_SECONDS":
		return &p.RecoverySeconds
	case "PROJECT_ONE_DISTRIBUTION_KEY_TTL_SECONDS":
		return &p.KeyTTLSeconds
	case "PROJECT_ONE_DISTRIBUTION_KEY_MAX_TTL_SECONDS":
		return &p.KeyMaxTTLSeconds
	case "PROJECT_ONE_DISTRIBUTION_KEY_MAX_ACTIVE":
		return &p.KeyMaxActive
	case "PROJECT_ONE_DISTRIBUTION_ALIAS_MAX_ACTIVE":
		return &p.AliasMaxActive
	case "PROJECT_ONE_DISTRIBUTION_MAX_CONCURRENT_REQUESTS":
		return &p.MaxConcurrentRequests
	case "PROJECT_ONE_DISTRIBUTION_REQUEST_SECONDS":
		return &p.RequestSeconds
	case "PROJECT_ONE_DISTRIBUTION_CONTEXT_TOKENS":
		return &p.ContextTokens
	case "PROJECT_ONE_DISTRIBUTION_OUTPUT_TOKENS":
		return &p.OutputTokens
	case "PROJECT_ONE_DISTRIBUTION_REQUESTS_PER_WINDOW":
		return &p.RequestsPerWindow
	case "PROJECT_ONE_DISTRIBUTION_RATE_WINDOW_SECONDS":
		return &p.RateWindowSeconds
	}
	return nil
}
