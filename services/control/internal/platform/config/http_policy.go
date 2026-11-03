package config

import (
	"errors"
	"go.yaml.in/yaml/v3"
	"reflect"
)

// DefaultHTTPPolicy is the single owner of default listener and retry budgets.
func DefaultHTTPPolicy() HTTPPolicy {
	return HTTPPolicy{InstallationLeaseIntervalSeconds: 5, StartupSeconds: 20, ReadHeaderSeconds: 5, RequestSeconds: 15,
		IdleSeconds: 60, ShutdownSeconds: 5, MaxConnections: 128, MaxActiveRequests: 64,
		RateLimitFallbackRetrySeconds: 900, ServiceUnavailableRetrySeconds: 2}
}

func (p HTTPPolicy) Validate() error {
	if p.InstallationLeaseIntervalSeconds < 1 || p.InstallationLeaseIntervalSeconds > 60 ||
		p.StartupSeconds < 1 || p.StartupSeconds > 300 ||
		p.ReadHeaderSeconds < 1 || p.ReadHeaderSeconds > 60 ||
		p.RequestSeconds < 1 || p.RequestSeconds > 300 ||
		p.IdleSeconds < 1 || p.IdleSeconds > 600 ||
		p.ShutdownSeconds < 1 || p.ShutdownSeconds > 120 ||
		p.MaxConnections < 1 || p.MaxConnections > 4096 ||
		p.MaxActiveRequests < 1 || p.MaxActiveRequests > 2048 ||
		p.RateLimitFallbackRetrySeconds < 1 || p.RateLimitFallbackRetrySeconds > 3600 ||
		p.ServiceUnavailableRetrySeconds < 1 || p.ServiceUnavailableRetrySeconds > 3600 {
		return errors.New("CONFIG_HTTP_BOUNDS_INVALID")
	}
	if p.MaxActiveRequests > p.MaxConnections || p.ReadHeaderSeconds > p.RequestSeconds {
		return errors.New("CONFIG_HTTP_RELATION_INVALID")
	}
	return nil
}

// Explicit policy members must retain their integer shape; null/aliases cannot
// silently inherit the default mandatory lease cadence.
func (p *HTTPPolicy) UnmarshalYAML(node *yaml.Node) error {
	if !modelPolicyAnchorsAbsent(node) || strictKnowledgeYAML(node, reflect.TypeOf(*p)) != nil {
		return errors.New("CONFIG_HTTP_BOUNDS_INVALID")
	}
	type plain HTTPPolicy
	value := plain(*p)
	if node.Decode(&value) != nil {
		return errors.New("CONFIG_HTTP_BOUNDS_INVALID")
	}
	*p = HTTPPolicy(value)
	return nil
}
