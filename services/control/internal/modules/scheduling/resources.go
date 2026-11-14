package scheduling

import (
	"errors"
	"math"
	"regexp"
	"strings"
)

var (
	// ErrInvalidResources indicates an invalid resource name or quantity.
	ErrInvalidResources = errors.New("SCHEDULING_INVALID_RESOURCES")
	// ErrResourceOverflow indicates that a total cannot be represented exactly.
	ErrResourceOverflow = errors.New("SCHEDULING_RESOURCE_OVERFLOW")
)

// Resource identifies a scheduling resource and determines its unit.
type Resource string

const (
	// CPU is counted in millicores.
	CPU Resource = "cpu"
	// Memory is counted in bytes.
	Memory Resource = "memory"
	// NVIDIAGPU is counted in whole devices.
	NVIDIAGPU Resource = "nvidia.com/gpu"
)

var (
	dnsLabel      = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	resourceLabel = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9_.-]{0,61}[A-Za-z0-9])?$`)
)

// Resources contains integral resource quantities. Absent keys represent zero.
type Resources map[Resource]int64

func (r Resource) valid() bool {
	if r == CPU || r == Memory {
		return true
	}
	domain, name, ok := strings.Cut(string(r), "/")
	if !ok || len(domain) > 253 || !strings.Contains(domain, ".") ||
		domain == "kubernetes.io" || strings.HasSuffix(domain, ".kubernetes.io") ||
		!resourceLabel.MatchString(name) {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if !dnsLabel.MatchString(label) {
			return false
		}
	}
	return true
}

func normalizeResources(in Resources) (Resources, error) {
	out := make(Resources, len(in))
	for resource, quantity := range in {
		if !resource.valid() || quantity < 0 {
			return nil, ErrInvalidResources
		}
		if quantity > 0 {
			out[resource] = quantity
		}
	}
	return out, nil
}

func (r Resources) fits(limit Resources) bool {
	for resource, quantity := range r {
		if quantity > limit[resource] {
			return false
		}
	}
	return true
}

func (r Resources) addScaled(other Resources, replicas int64) error {
	for resource, quantity := range other {
		if quantity > (math.MaxInt64-r[resource])/replicas {
			return ErrResourceOverflow
		}
		r[resource] += quantity * replicas
	}
	return nil
}
