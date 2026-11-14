package http

import "github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"

type taskDeliveryRuntimePolicy struct {
	Enabled             bool `json:"enabled"`
	RequestMilliseconds int  `json:"request_timeout_milliseconds"`
}

func taskDeliveryPublicPolicy(c config.Config) taskDeliveryRuntimePolicy {
	if !c.TaskEvents.Enabled {
		return taskDeliveryRuntimePolicy{}
	}
	return taskDeliveryRuntimePolicy{Enabled: true, RequestMilliseconds: c.TaskEvents.RequestSeconds * 1000}
}
