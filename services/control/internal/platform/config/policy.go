// Package config owns validated, restart-required runtime settings.
package config

import (
	"errors"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	gatewaywire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	"time"
)

type RuntimePolicy struct {
	Workspaces     WorkspacePolicy      `yaml:"workspaces"`
	Licensing      *LicensePolicy       `yaml:"licensing,omitempty" json:"Licensing,omitempty"`
	Localization   LocalizationPolicy   `yaml:"localization"`
	Portal         PortalPolicy         `yaml:"portal"`
	Plugins        PluginPolicy         `yaml:"plugins"`
	Workflows      WorkflowPolicy       `yaml:"workflows"`
	Knowledge      KnowledgePolicy      `yaml:"knowledge"`
	SSO            SSOPolicy            `yaml:"sso"`
	ModelLifecycle ModelLifecyclePolicy `yaml:"model_lifecycle"`
	Training       TrainingPolicy       `yaml:"training"`
	Datasets       DatasetPolicy        `yaml:"datasets"`
	UI             UIPolicy             `yaml:"ui"`
	HTTP           HTTPPolicy           `yaml:"http"`
	Identity       IdentityPolicy       `yaml:"identity"`
	Mail           MailPolicy           `yaml:"mail"`
	Retention      RetentionPolicy      `yaml:"retention"`
	Nodes          NodesPolicy          `yaml:"nodes"`
	Models         ModelsPolicy         `yaml:"models"`
	Distribution   DistributionPolicy   `yaml:"distribution"`
	Rates          RatesPolicy          `yaml:"rates"`
	AgentAdmission AgentAdmissionPolicy `yaml:"agent_admission"`
}
type HTTPPolicy struct {
	InstallationLeaseIntervalSeconds int `yaml:"installation_lease_interval_seconds"`
	StartupSeconds                   int `yaml:"startup_seconds"`
	ReadHeaderSeconds                int `yaml:"read_header_seconds"`
	RequestSeconds                   int `yaml:"request_seconds"`
	IdleSeconds                      int `yaml:"idle_seconds"`
	ShutdownSeconds                  int `yaml:"shutdown_seconds"`
	MaxConnections                   int `yaml:"max_connections"`
	MaxActiveRequests                int `yaml:"max_active_requests"`
	RateLimitFallbackRetrySeconds    int `yaml:"rate_limit_fallback_retry_seconds"`
	ServiceUnavailableRetrySeconds   int `yaml:"service_unavailable_retry_seconds"`
}
type IdentityPolicy struct {
	InvitationTTLSeconds      int `yaml:"invitation_ttl_seconds"`
	ResetTTLSeconds           int `yaml:"reset_ttl_seconds"`
	PortalIdleSeconds         int `yaml:"portal_idle_seconds"`
	PortalAbsoluteSeconds     int `yaml:"portal_absolute_seconds"`
	AdminIdleSeconds          int `yaml:"admin_idle_seconds"`
	AdminAbsoluteSeconds      int `yaml:"admin_absolute_seconds"`
	CSRFSeconds               int `yaml:"csrf_seconds"`
	SessionTouchSeconds       int `yaml:"session_touch_seconds"`
	IdempotencySeconds        int `yaml:"idempotency_seconds"`
	HashWorkers               int `yaml:"hash_workers"`
	HashQueueCapacity         int `yaml:"hash_queue_capacity"`
	HashAdmissionMilliseconds int `yaml:"hash_admission_milliseconds"`
	MaxLiveResetTokens        int `yaml:"max_live_reset_tokens"`
	ResetMinimumMilliseconds  int `yaml:"reset_minimum_milliseconds"`
	ResetJitterMilliseconds   int `yaml:"reset_jitter_milliseconds"`
	AuditDefaultDays          int `yaml:"audit_default_days"`
	AuditMaxDays              int `yaml:"audit_max_days"`
}
type MailPolicy struct {
	PollMilliseconds int `yaml:"poll_milliseconds"`
	AttemptSeconds   int `yaml:"attempt_seconds"`
	LeaseSeconds     int `yaml:"lease_seconds"`
	MaxAttempts      int `yaml:"max_attempts"`
	Retry1Seconds    int `yaml:"retry_1_seconds"`
	Retry2Seconds    int `yaml:"retry_2_seconds"`
	Retry3Seconds    int `yaml:"retry_3_seconds"`
	Retry4Seconds    int `yaml:"retry_4_seconds"`
	JitterPercent    int `yaml:"jitter_percent"`
}
type RetentionPolicy struct {
	IntervalSeconds  int `yaml:"interval_seconds"`
	TimeoutSeconds   int `yaml:"timeout_seconds"`
	BatchSize        int `yaml:"batch_size"`
	MailDays         int `yaml:"mail_days"`
	ResetDays        int `yaml:"reset_days"`
	InvitationDays   int `yaml:"invitation_days"`
	SessionDays      int `yaml:"session_days"`
	RateGraceSeconds int `yaml:"rate_grace_seconds"`
	AuditDays        int `yaml:"audit_days"`
}
type NodesPolicy struct {
	MaximumLiveNodes        int `yaml:"maximum_live_nodes"`
	MaximumLiveJoinTokens   int `yaml:"maximum_live_join_tokens"`
	JoinTTLSeconds          int `yaml:"join_ttl_seconds"`
	OnlineSeconds           int `yaml:"online_seconds"`
	OfflineSeconds          int `yaml:"offline_seconds"`
	TaskSeconds             int `yaml:"task_seconds"`
	InferenceDeploySeconds  int `yaml:"inference_deploy_seconds"`
	TaskLeaseSeconds        int `yaml:"task_lease_seconds"`
	MaximumTaskAttempts     int `yaml:"maximum_task_attempts"`
	TaskSweepBatch          int `yaml:"task_sweep_batch"`
	ReconcileSeconds        int `yaml:"reconcile_seconds"`
	ReconcileTimeoutSeconds int `yaml:"reconcile_timeout_seconds"`
	// MaximumTaskDeliveries bounds how often a lease that was never acknowledged is
	// delivered again before the task fails (ADR 0011).
	MaximumTaskDeliveries int `yaml:"maximum_task_deliveries"`
	// ResyncIntervalSeconds is the shortest time between two full resyncs of one node;
	// ResyncPerSecond is the fleet-wide rate at which control grants them (ADR 0011).
	ResyncIntervalSeconds int `yaml:"resync_interval_seconds"`
	ResyncPerSecond       int `yaml:"resync_per_second"`
	// LivenessFlushSeconds is how often control writes last-seen times and the latest
	// reports to the database; StateRefreshSeconds is the longest a node's state goes
	// without a synchronous write and its checks (ADR 0022).
	LivenessFlushSeconds int `yaml:"liveness_flush_seconds"`
	StateRefreshSeconds  int `yaml:"state_refresh_seconds"`
}

// PacingWindowSeconds is the part of the online window a heartbeat interval may use: a
// last-seen time reaches the database up to one flush after the heartbeat.
func (p NodesPolicy) PacingWindowSeconds() int { return p.OnlineSeconds - p.LivenessFlushSeconds }

type DistributionPolicy struct {
	RecoverySeconds       int `yaml:"recovery_seconds"`
	KeyTTLSeconds         int `yaml:"key_ttl_seconds"`
	KeyMaxTTLSeconds      int `yaml:"key_max_ttl_seconds"`
	KeyMaxActive          int `yaml:"key_max_active"`
	AliasMaxActive        int `yaml:"alias_max_active"`
	MaxConcurrentRequests int `yaml:"max_concurrent_requests"`
	RequestSeconds        int `yaml:"request_seconds"`
	ContextTokens         int `yaml:"context_tokens"`
	OutputTokens          int `yaml:"output_tokens"`
	RequestsPerWindow     int `yaml:"requests_per_window"`
	RateWindowSeconds     int `yaml:"rate_window_seconds"`
}
type RatesPolicy struct {
	CSRFCount                  int `yaml:"csrf_count"`
	CSRFWindowSeconds          int `yaml:"csrf_window_seconds"`
	LoginEmailCount            int `yaml:"login_email_count"`
	LoginEmailWindowSeconds    int `yaml:"login_email_window_seconds"`
	LoginIPCount               int `yaml:"login_ip_count"`
	LoginIPWindowSeconds       int `yaml:"login_ip_window_seconds"`
	ResetIPCount               int `yaml:"reset_ip_count"`
	ResetIPWindowSeconds       int `yaml:"reset_ip_window_seconds"`
	ResetEmailCount            int `yaml:"reset_email_count"`
	ResetEmailWindowSeconds    int `yaml:"reset_email_window_seconds"`
	TokenIPCount               int `yaml:"token_ip_count"`
	TokenIPWindowSeconds       int `yaml:"token_ip_window_seconds"`
	AdminInviteCount           int `yaml:"admin_invite_count"`
	AdminInviteWindowSeconds   int `yaml:"admin_invite_window_seconds"`
	AgentEnrollIPCount         int `yaml:"agent_enroll_ip_count"`
	AgentEnrollIPWindowSeconds int `yaml:"agent_enroll_ip_window_seconds"`
	ResetCooldownSeconds       int `yaml:"reset_cooldown_seconds"`
}

func DefaultRuntimePolicy() RuntimePolicy {
	return RuntimePolicy{
		Workspaces:     DefaultWorkspacePolicy(),
		Localization:   DefaultLocalizationPolicy(),
		Portal:         DefaultPortalPolicy(),
		Plugins:        DefaultPluginPolicy(),
		Workflows:      DefaultWorkflowPolicy(),
		Knowledge:      DefaultKnowledgePolicy(),
		SSO:            DefaultSSOPolicy(),
		ModelLifecycle: DefaultModelLifecyclePolicy(),
		Training:       DefaultTrainingPolicy(),
		Datasets:       DefaultDatasetPolicy(),
		UI:             UIPolicy{PollMilliseconds: 5000, SearchDebounceMilliseconds: 150, PageSize: 20, LookupPageSize: 100},
		HTTP:           DefaultHTTPPolicy(),
		Identity:       IdentityPolicy{InvitationTTLSeconds: 86400, ResetTTLSeconds: 900, PortalIdleSeconds: 1800, PortalAbsoluteSeconds: 43200, AdminIdleSeconds: 900, AdminAbsoluteSeconds: 14400, CSRFSeconds: 900, SessionTouchSeconds: 60, IdempotencySeconds: 86400, HashWorkers: 2, HashQueueCapacity: 16, HashAdmissionMilliseconds: 2000, MaxLiveResetTokens: 3, ResetMinimumMilliseconds: 300, ResetJitterMilliseconds: 50, AuditDefaultDays: 7, AuditMaxDays: 31},
		Mail:           MailPolicy{PollMilliseconds: 1000, AttemptSeconds: 25, LeaseSeconds: 60, MaxAttempts: 5, Retry1Seconds: 15, Retry2Seconds: 60, Retry3Seconds: 180, Retry4Seconds: 300, JitterPercent: 10},
		Retention:      RetentionPolicy{IntervalSeconds: 60, TimeoutSeconds: 5, BatchSize: 100, MailDays: 30, ResetDays: 30, InvitationDays: 90, SessionDays: 7, RateGraceSeconds: 3600, AuditDays: 180},
		Nodes:          NodesPolicy{MaximumLiveNodes: 100, MaximumLiveJoinTokens: 100, JoinTTLSeconds: 600, OnlineSeconds: 30, OfflineSeconds: 120, TaskSeconds: 300, InferenceDeploySeconds: 1800, TaskLeaseSeconds: 30, MaximumTaskAttempts: 3, TaskSweepBatch: 100, ReconcileSeconds: 10, ReconcileTimeoutSeconds: 5, MaximumTaskDeliveries: 10, ResyncIntervalSeconds: 300, ResyncPerSecond: 20, LivenessFlushSeconds: 3, StateRefreshSeconds: 300},
		Models:         DefaultModelsPolicy(),
		Distribution:   DistributionPolicy{RecoverySeconds: 600, KeyTTLSeconds: 86400, KeyMaxTTLSeconds: 7776000, KeyMaxActive: 100, AliasMaxActive: 16, MaxConcurrentRequests: 4, RequestSeconds: 90, ContextTokens: 2048, OutputTokens: 512, RequestsPerWindow: 60, RateWindowSeconds: 60},
		AgentAdmission: DefaultAgentAdmissionPolicy(),
		Rates:          RatesPolicy{CSRFCount: 60, CSRFWindowSeconds: 60, LoginEmailCount: 10, LoginEmailWindowSeconds: 900, LoginIPCount: 30, LoginIPWindowSeconds: 300, ResetIPCount: 20, ResetIPWindowSeconds: 900, ResetEmailCount: 3, ResetEmailWindowSeconds: 900, TokenIPCount: 30, TokenIPWindowSeconds: 900, AdminInviteCount: 30, AdminInviteWindowSeconds: 3600, AgentEnrollIPCount: 20, AgentEnrollIPWindowSeconds: 60, ResetCooldownSeconds: 60},
	}
}

// Policy returns a value copy. Nil is the backward-compatible default for in-process callers.
func (c Config) Policy() RuntimePolicy {
	if c.Runtime == nil {
		return DefaultRuntimePolicy()
	}
	return *c.Runtime
}
func Seconds(n int) time.Duration      { return time.Duration(n) * time.Second }
func Milliseconds(n int) time.Duration { return time.Duration(n) * time.Millisecond }
func (p RuntimePolicy) Validate() error {
	if e := p.Workspaces.Validate(); e != nil {
		return e
	}
	if p.Licensing != nil && p.Licensing.Validate() != nil {
		return errors.New("CONFIG_LICENSING_POLICY_INVALID")
	}
	if e := p.Localization.Validate(); e != nil {
		return e
	}
	if e := p.Portal.Validate(); e != nil {
		return e
	}
	if e := p.Plugins.Validate(); e != nil {
		return e
	}
	if e := p.Workflows.Validate(); e != nil {
		return e
	}
	if e := p.Knowledge.Validate(); e != nil {
		return e
	}
	if e := p.SSO.Validate(); e != nil {
		return e
	}
	if e := p.ModelLifecycle.Validate(); e != nil {
		return e
	}
	if e := p.Training.Validate(); e != nil {
		return e
	}
	if e := p.Datasets.Validate(); e != nil {
		return e
	}
	if e := p.Distribution.Validate(); e != nil {
		return e
	}
	if p.UI.PollMilliseconds < 1000 || p.UI.PollMilliseconds > 60000 || p.UI.SearchDebounceMilliseconds < 0 || p.UI.SearchDebounceMilliseconds > 2000 || p.UI.PageSize < 1 || p.UI.PageSize > 100 || p.UI.LookupPageSize < 1 || p.UI.LookupPageSize > 100 {
		return errors.New("CONFIG_UI_BOUNDS_INVALID")
	}
	if e := p.HTTP.Validate(); e != nil {
		return e
	}
	if p.Identity.InvitationTTLSeconds < 300 || p.Identity.InvitationTTLSeconds > 604800 {
		return errors.New("CONFIG_IDENTITY_BOUNDS_INVALID")
	}
	if p.Identity.ResetTTLSeconds < 60 || p.Identity.ResetTTLSeconds > 3600 {
		return errors.New("CONFIG_IDENTITY_BOUNDS_INVALID")
	}
	if p.Identity.PortalIdleSeconds < 60 || p.Identity.PortalIdleSeconds > 86400 {
		return errors.New("CONFIG_IDENTITY_BOUNDS_INVALID")
	}
	if p.Identity.PortalAbsoluteSeconds < 120 || p.Identity.PortalAbsoluteSeconds > 604800 {
		return errors.New("CONFIG_IDENTITY_BOUNDS_INVALID")
	}
	if p.Identity.AdminIdleSeconds < 60 || p.Identity.AdminIdleSeconds > 3600 {
		return errors.New("CONFIG_IDENTITY_BOUNDS_INVALID")
	}
	if p.Identity.AdminAbsoluteSeconds < 120 || p.Identity.AdminAbsoluteSeconds > 86400 {
		return errors.New("CONFIG_IDENTITY_BOUNDS_INVALID")
	}
	if p.Identity.CSRFSeconds < 60 || p.Identity.CSRFSeconds > 3600 {
		return errors.New("CONFIG_IDENTITY_BOUNDS_INVALID")
	}
	if p.Identity.SessionTouchSeconds < 1 || p.Identity.SessionTouchSeconds > 300 {
		return errors.New("CONFIG_IDENTITY_BOUNDS_INVALID")
	}
	if p.Identity.IdempotencySeconds < 60 || p.Identity.IdempotencySeconds > 604800 {
		return errors.New("CONFIG_IDENTITY_BOUNDS_INVALID")
	}
	if p.Identity.HashWorkers < 1 || p.Identity.HashWorkers > 8 {
		return errors.New("CONFIG_IDENTITY_BOUNDS_INVALID")
	}
	if p.Identity.HashQueueCapacity < 0 || p.Identity.HashQueueCapacity > 128 {
		return errors.New("CONFIG_IDENTITY_BOUNDS_INVALID")
	}
	if p.Identity.HashAdmissionMilliseconds < 100 || p.Identity.HashAdmissionMilliseconds > 10000 {
		return errors.New("CONFIG_IDENTITY_BOUNDS_INVALID")
	}
	if p.Identity.MaxLiveResetTokens < 1 || p.Identity.MaxLiveResetTokens > 10 {
		return errors.New("CONFIG_IDENTITY_BOUNDS_INVALID")
	}
	if p.Identity.ResetMinimumMilliseconds < 300 || p.Identity.ResetMinimumMilliseconds > 3000 {
		return errors.New("CONFIG_IDENTITY_BOUNDS_INVALID")
	}
	if p.Identity.ResetJitterMilliseconds < 0 || p.Identity.ResetJitterMilliseconds > 1000 {
		return errors.New("CONFIG_IDENTITY_BOUNDS_INVALID")
	}
	if p.Identity.AuditDefaultDays < 1 || p.Identity.AuditDefaultDays > 31 {
		return errors.New("CONFIG_IDENTITY_BOUNDS_INVALID")
	}
	if p.Identity.AuditMaxDays < 1 || p.Identity.AuditMaxDays > 366 {
		return errors.New("CONFIG_IDENTITY_BOUNDS_INVALID")
	}
	if p.Mail.PollMilliseconds < 100 || p.Mail.PollMilliseconds > 60000 {
		return errors.New("CONFIG_MAIL_BOUNDS_INVALID")
	}
	if p.Mail.AttemptSeconds < 5 || p.Mail.AttemptSeconds > 120 {
		return errors.New("CONFIG_MAIL_BOUNDS_INVALID")
	}
	if p.Mail.LeaseSeconds < 10 || p.Mail.LeaseSeconds > 300 {
		return errors.New("CONFIG_MAIL_BOUNDS_INVALID")
	}
	if p.Mail.MaxAttempts < 1 || p.Mail.MaxAttempts > 5 {
		return errors.New("CONFIG_MAIL_BOUNDS_INVALID")
	}
	if p.Mail.Retry1Seconds < 1 || p.Mail.Retry1Seconds > 3600 {
		return errors.New("CONFIG_MAIL_BOUNDS_INVALID")
	}
	if p.Mail.Retry2Seconds < 1 || p.Mail.Retry2Seconds > 3600 {
		return errors.New("CONFIG_MAIL_BOUNDS_INVALID")
	}
	if p.Mail.Retry3Seconds < 1 || p.Mail.Retry3Seconds > 3600 {
		return errors.New("CONFIG_MAIL_BOUNDS_INVALID")
	}
	if p.Mail.Retry4Seconds < 1 || p.Mail.Retry4Seconds > 3600 {
		return errors.New("CONFIG_MAIL_BOUNDS_INVALID")
	}
	if p.Mail.JitterPercent < 0 || p.Mail.JitterPercent > 25 {
		return errors.New("CONFIG_MAIL_BOUNDS_INVALID")
	}
	if p.Retention.IntervalSeconds < 1 || p.Retention.IntervalSeconds > 3600 {
		return errors.New("CONFIG_RETENTION_BOUNDS_INVALID")
	}
	if p.Retention.TimeoutSeconds < 1 || p.Retention.TimeoutSeconds > 60 {
		return errors.New("CONFIG_RETENTION_BOUNDS_INVALID")
	}
	if p.Retention.BatchSize < 1 || p.Retention.BatchSize > 10000 {
		return errors.New("CONFIG_RETENTION_BOUNDS_INVALID")
	}
	if p.Retention.MailDays < 1 || p.Retention.MailDays > 3650 {
		return errors.New("CONFIG_RETENTION_BOUNDS_INVALID")
	}
	if p.Retention.ResetDays < 1 || p.Retention.ResetDays > 3650 {
		return errors.New("CONFIG_RETENTION_BOUNDS_INVALID")
	}
	if p.Retention.InvitationDays < 1 || p.Retention.InvitationDays > 3650 {
		return errors.New("CONFIG_RETENTION_BOUNDS_INVALID")
	}
	if p.Retention.SessionDays < 1 || p.Retention.SessionDays > 3650 {
		return errors.New("CONFIG_RETENTION_BOUNDS_INVALID")
	}
	if p.Retention.RateGraceSeconds < 1 || p.Retention.RateGraceSeconds > 86400 {
		return errors.New("CONFIG_RETENTION_BOUNDS_INVALID")
	}
	if p.Retention.AuditDays < 1 || p.Retention.AuditDays > 3650 {
		return errors.New("CONFIG_RETENTION_BOUNDS_INVALID")
	}
	if p.Nodes.MaximumLiveNodes < 1 || p.Nodes.MaximumLiveNodes > 10000 {
		return errors.New("CONFIG_NODES_BOUNDS_INVALID")
	}
	if p.Nodes.MaximumLiveJoinTokens < 1 || p.Nodes.MaximumLiveJoinTokens > 10000 {
		return errors.New("CONFIG_NODES_BOUNDS_INVALID")
	}
	if p.Nodes.JoinTTLSeconds < 60 || p.Nodes.JoinTTLSeconds > 3600 {
		return errors.New("CONFIG_NODES_BOUNDS_INVALID")
	}
	if p.Nodes.OnlineSeconds < 20 || p.Nodes.OnlineSeconds > 300 {
		return errors.New("CONFIG_NODES_BOUNDS_INVALID")
	}
	if p.Nodes.OfflineSeconds < 30 || p.Nodes.OfflineSeconds > 3600 {
		return errors.New("CONFIG_NODES_BOUNDS_INVALID")
	}
	if p.Nodes.TaskSeconds < 120 || p.Nodes.TaskSeconds > 1800 {
		return errors.New("CONFIG_NODES_BOUNDS_INVALID")
	}
	if p.Nodes.InferenceDeploySeconds < 120 || p.Nodes.InferenceDeploySeconds > 1800 || p.Nodes.InferenceDeploySeconds < p.Nodes.TaskSeconds ||
		p.Nodes.TaskLeaseSeconds < 10 || p.Nodes.TaskLeaseSeconds > agentwire.TaskLeaseSeconds ||
		p.Nodes.MaximumTaskAttempts < 1 || p.Nodes.MaximumTaskAttempts > 3 || p.Nodes.TaskSweepBatch < 1 || p.Nodes.TaskSweepBatch > 1000 {
		return errors.New("CONFIG_NODES_BOUNDS_INVALID")
	}
	if p.Nodes.LivenessFlushSeconds < 1 || p.Nodes.LivenessFlushSeconds > 10 || p.Nodes.StateRefreshSeconds < 30 || p.Nodes.StateRefreshSeconds > 3600 || p.Nodes.PacingWindowSeconds() < 1 || !agentwire.DefaultPacing().FitsOnlineWindow(p.Nodes.PacingWindowSeconds()) {
		return errors.New("CONFIG_NODES_BOUNDS_INVALID")
	}
	if p.Nodes.ReconcileSeconds < 1 || p.Nodes.ReconcileSeconds > 60 {
		return errors.New("CONFIG_NODES_BOUNDS_INVALID")
	}
	if p.Nodes.ReconcileTimeoutSeconds < 1 || p.Nodes.ReconcileTimeoutSeconds > 30 {
		return errors.New("CONFIG_NODES_BOUNDS_INVALID")
	}
	if p.Nodes.MaximumTaskDeliveries < 1 || p.Nodes.MaximumTaskDeliveries > 100 || p.Nodes.ResyncIntervalSeconds < 10 || p.Nodes.ResyncIntervalSeconds > 86400 || p.Nodes.ResyncPerSecond < 1 || p.Nodes.ResyncPerSecond > 10000 {
		return errors.New("CONFIG_NODES_BOUNDS_INVALID")
	}
	if e := p.Models.Validate(); e != nil {
		return e
	}
	if p.Rates.CSRFCount < 1 || p.Rates.CSRFCount > 10000 {
		return errors.New("CONFIG_RATES_BOUNDS_INVALID")
	}
	if p.Rates.CSRFWindowSeconds < 1 || p.Rates.CSRFWindowSeconds > 86400 {
		return errors.New("CONFIG_RATES_BOUNDS_INVALID")
	}
	if p.Rates.LoginEmailCount < 1 || p.Rates.LoginEmailCount > 10000 {
		return errors.New("CONFIG_RATES_BOUNDS_INVALID")
	}
	if p.Rates.LoginEmailWindowSeconds < 1 || p.Rates.LoginEmailWindowSeconds > 86400 {
		return errors.New("CONFIG_RATES_BOUNDS_INVALID")
	}
	if p.Rates.LoginIPCount < 1 || p.Rates.LoginIPCount > 10000 {
		return errors.New("CONFIG_RATES_BOUNDS_INVALID")
	}
	if p.Rates.LoginIPWindowSeconds < 1 || p.Rates.LoginIPWindowSeconds > 86400 {
		return errors.New("CONFIG_RATES_BOUNDS_INVALID")
	}
	if p.Rates.ResetIPCount < 1 || p.Rates.ResetIPCount > 10000 {
		return errors.New("CONFIG_RATES_BOUNDS_INVALID")
	}
	if p.Rates.ResetIPWindowSeconds < 1 || p.Rates.ResetIPWindowSeconds > 86400 {
		return errors.New("CONFIG_RATES_BOUNDS_INVALID")
	}
	if p.Rates.ResetEmailCount < 1 || p.Rates.ResetEmailCount > 10000 {
		return errors.New("CONFIG_RATES_BOUNDS_INVALID")
	}
	if p.Rates.ResetEmailWindowSeconds < 1 || p.Rates.ResetEmailWindowSeconds > 86400 {
		return errors.New("CONFIG_RATES_BOUNDS_INVALID")
	}
	if p.Rates.TokenIPCount < 1 || p.Rates.TokenIPCount > 10000 {
		return errors.New("CONFIG_RATES_BOUNDS_INVALID")
	}
	if p.Rates.TokenIPWindowSeconds < 1 || p.Rates.TokenIPWindowSeconds > 86400 {
		return errors.New("CONFIG_RATES_BOUNDS_INVALID")
	}
	if p.Rates.AdminInviteCount < 1 || p.Rates.AdminInviteCount > 10000 {
		return errors.New("CONFIG_RATES_BOUNDS_INVALID")
	}
	if p.Rates.AdminInviteWindowSeconds < 1 || p.Rates.AdminInviteWindowSeconds > 86400 {
		return errors.New("CONFIG_RATES_BOUNDS_INVALID")
	}
	if p.Rates.AgentEnrollIPCount < 1 || p.Rates.AgentEnrollIPCount > 10000 {
		return errors.New("CONFIG_RATES_BOUNDS_INVALID")
	}
	if p.Rates.AgentEnrollIPWindowSeconds < 1 || p.Rates.AgentEnrollIPWindowSeconds > 86400 {
		return errors.New("CONFIG_RATES_BOUNDS_INVALID")
	}
	if p.Rates.ResetCooldownSeconds < 1 || p.Rates.ResetCooldownSeconds > 3600 {
		return errors.New("CONFIG_RATES_BOUNDS_INVALID")
	}
	if e := p.AgentAdmission.Validate(); e != nil {
		return e
	}
	if p.HTTP.RequestSeconds*1000 <= p.Identity.ResetMinimumMilliseconds+p.Identity.ResetJitterMilliseconds {
		return errors.New("CONFIG_REQUEST_DEADLINE_INVALID")
	}
	if p.Identity.PortalIdleSeconds >= p.Identity.PortalAbsoluteSeconds || p.Identity.AdminIdleSeconds >= p.Identity.AdminAbsoluteSeconds || p.Identity.SessionTouchSeconds >= p.Identity.AdminIdleSeconds || p.Identity.SessionTouchSeconds >= p.Identity.PortalIdleSeconds || p.Identity.AuditDefaultDays > p.Identity.AuditMaxDays {
		return errors.New("CONFIG_IDENTITY_RELATION_INVALID")
	}
	if p.Mail.LeaseSeconds <= p.Mail.AttemptSeconds || p.Mail.Retry1Seconds > p.Mail.Retry2Seconds || p.Mail.Retry2Seconds > p.Mail.Retry3Seconds || p.Mail.Retry3Seconds > p.Mail.Retry4Seconds {
		return errors.New("CONFIG_MAIL_RELATION_INVALID")
	}
	if p.Nodes.OfflineSeconds <= p.Nodes.OnlineSeconds || p.Rates.ResetCooldownSeconds > p.Rates.ResetEmailWindowSeconds {
		return errors.New("CONFIG_POLICY_RELATION_INVALID")
	}
	return nil
}

func (p DistributionPolicy) Validate() error {
	if p.RecoverySeconds < 30 || p.RecoverySeconds > 3600 {
		return errors.New("CONFIG_DISTRIBUTION_BOUNDS_INVALID")
	}
	recipe := agentwire.ApprovedInferenceRecipe()
	const maximumKeyTTLSeconds = 90 * 24 * 60 * 60
	if p.KeyTTLSeconds < 300 || p.KeyTTLSeconds > p.KeyMaxTTLSeconds || p.KeyMaxTTLSeconds > maximumKeyTTLSeconds || p.KeyMaxActive < 1 || p.KeyMaxActive > 1000 || p.AliasMaxActive < 1 || p.AliasMaxActive > gatewaywire.MaxModelBindings || p.MaxConcurrentRequests < 1 || p.MaxConcurrentRequests > recipe.MaximumSequences || p.RequestSeconds < 1 || uint64(p.RequestSeconds) > gatewaywire.MaxRequestSeconds || p.ContextTokens < 1 || p.ContextTokens > recipe.MaximumContext || p.OutputTokens < 1 || uint32(p.OutputTokens) > gatewaywire.MaxOutputTokens || p.OutputTokens > p.ContextTokens || p.RequestsPerWindow < 1 || p.RequestsPerWindow > 10000 || p.RateWindowSeconds < 1 || p.RateWindowSeconds > 86400 {
		return errors.New("CONFIG_DISTRIBUTION_BOUNDS_INVALID")
	}
	return nil
}

// UIPolicy is the explicit public allowlist; never serialize Config to the browser.
type UIPolicy struct {
	PollMilliseconds           int `yaml:"poll_milliseconds" json:"poll_milliseconds"`
	SearchDebounceMilliseconds int `yaml:"search_debounce_milliseconds" json:"search_debounce_milliseconds"`
	PageSize                   int `yaml:"page_size" json:"page_size"`
	LookupPageSize             int `yaml:"lookup_page_size" json:"lookup_page_size"`
}
