package config

const (
	nodeMaximumLiveNodesEnvironment    = "PROJECT_ONE_NODE_MAXIMUM_LIVE_NODES"
	nodeMaximumLiveJoinEnvironment     = "PROJECT_ONE_NODE_MAXIMUM_LIVE_JOIN_TOKENS"
	nodeJoinTTLEnvironment             = "PROJECT_ONE_NODE_JOIN_TTL_SECONDS"
	nodeOnlineEnvironment              = "PROJECT_ONE_NODE_ONLINE_SECONDS"
	nodeOfflineEnvironment             = "PROJECT_ONE_NODE_OFFLINE_SECONDS"
	nodeInferenceDeployEnvironment     = "PROJECT_ONE_NODE_INFERENCE_DEPLOY_SECONDS"
	nodeTaskLeaseEnvironment           = "PROJECT_ONE_NODE_TASK_LEASE_SECONDS"
	nodeMaximumTaskAttemptsEnvironment = "PROJECT_ONE_NODE_MAXIMUM_TASK_ATTEMPTS"
	nodeTaskSweepBatchEnvironment      = "PROJECT_ONE_NODE_TASK_SWEEP_BATCH"
	nodeTaskEnvironment                = "PROJECT_ONE_NODE_TASK_SECONDS"
	nodeLivenessFlushEnvironment       = "PROJECT_ONE_NODE_LIVENESS_FLUSH_SECONDS"
	nodeStateRefreshEnvironment        = "PROJECT_ONE_NODE_STATE_REFRESH_SECONDS"
	nodeReconcileEnvironment           = "PROJECT_ONE_NODE_RECONCILE_SECONDS"
	nodeReconcileTimeoutEnvironment    = "PROJECT_ONE_NODE_RECONCILE_TIMEOUT_SECONDS"
	uiPageEnvironment                  = "PROJECT_ONE_UI_PAGE_SIZE"
	uiPollEnvironment                  = "PROJECT_ONE_UI_POLL_MILLISECONDS"
	uiLookupEnvironment                = "PROJECT_ONE_UI_LOOKUP_PAGE_SIZE"
)

// nodeEnvironmentField maps the existing private policy input into its owning
// typed field. Defaults and range/cross-field validation remain in RuntimePolicy.
func (p *RuntimePolicy) nodeEnvironmentField(key string) *int {
	switch key {
	case nodeMaximumLiveNodesEnvironment:
		return &p.Nodes.MaximumLiveNodes
	case nodeMaximumLiveJoinEnvironment:
		return &p.Nodes.MaximumLiveJoinTokens
	case nodeJoinTTLEnvironment:
		return &p.Nodes.JoinTTLSeconds
	case nodeOnlineEnvironment:
		return &p.Nodes.OnlineSeconds
	case nodeOfflineEnvironment:
		return &p.Nodes.OfflineSeconds
	case nodeInferenceDeployEnvironment:
		return &p.Nodes.InferenceDeploySeconds
	case nodeTaskLeaseEnvironment:
		return &p.Nodes.TaskLeaseSeconds
	case nodeMaximumTaskAttemptsEnvironment:
		return &p.Nodes.MaximumTaskAttempts
	case nodeTaskSweepBatchEnvironment:
		return &p.Nodes.TaskSweepBatch
	case nodeTaskEnvironment:
		return &p.Nodes.TaskSeconds
	case nodeLivenessFlushEnvironment:
		return &p.Nodes.LivenessFlushSeconds
	case nodeStateRefreshEnvironment:
		return &p.Nodes.StateRefreshSeconds
	case nodeReconcileEnvironment:
		return &p.Nodes.ReconcileSeconds
	case nodeReconcileTimeoutEnvironment:
		return &p.Nodes.ReconcileTimeoutSeconds
	case uiPageEnvironment:
		return &p.UI.PageSize
	case uiPollEnvironment:
		return &p.UI.PollMilliseconds
	case uiLookupEnvironment:
		return &p.UI.LookupPageSize
	default:
		return nil
	}
}
