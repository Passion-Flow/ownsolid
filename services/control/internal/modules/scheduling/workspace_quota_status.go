package scheduling

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// WorkspaceQuotaState distinguishes missing configuration from a real allocation.
type WorkspaceQuotaState string

const (
	// WorkspaceQuotaUnconfigured means no platform allocation policy is installed.
	WorkspaceQuotaUnconfigured WorkspaceQuotaState = "unconfigured"
	// WorkspaceQuotaUnallocated means this authorized workspace has no allocation.
	WorkspaceQuotaUnallocated WorkspaceQuotaState = "unallocated"
	// WorkspaceQuotaConfigured means the resource rows describe an actual allocation.
	WorkspaceQuotaConfigured WorkspaceQuotaState = "configured"
)

// WorkspaceQuotaReason contains fixed guidance without private diagnostic text.
type WorkspaceQuotaReason struct {
	Code   string     `json:"code"`
	Action WaitAction `json:"action"`
}

// WorkspaceQuotaResource accounts for one resource in its native integer units.
// Quantities are decimal strings to preserve int64 precision in browser JSON.
// UnreservedQuota does not establish physical availability or job admission.
type WorkspaceQuotaResource struct {
	Resource        Resource `json:"resource"`
	Guaranteed      string   `json:"guaranteed"`
	HardLimit       string   `json:"hard_limit"`
	Reserved        string   `json:"reserved"`
	UnreservedQuota string   `json:"unreserved_quota"`
}

// WorkspaceQuotaStatus contains only the subject workspace's configured quota
// and held usage. Missing configuration has no resource rows.
type WorkspaceQuotaStatus struct {
	WorkspaceID    string                   `json:"workspace_id"`
	PolicyRevision int64                    `json:"policy_revision"`
	State          WorkspaceQuotaState      `json:"state"`
	Resources      []WorkspaceQuotaResource `json:"resources,omitempty"`
	Reason         *WorkspaceQuotaReason    `json:"reason,omitempty"`
}

// ReadWorkspaceQuotaStatus rechecks the authenticated actor and Workspace scope
// through the selected repository, then reads quota and reservations under the
// same transaction's policy/workspace fences. Only confirmed reads return data.
func ReadWorkspaceQuotaStatus(ctx context.Context, authority *workspaces.Service, actor identity.Auth, workspace string) (WorkspaceQuotaStatus, error) {
	if ctx == nil || authority == nil || authority.Identity == nil {
		return WorkspaceQuotaStatus{}, ErrQuotaPolicyUnavailable
	}
	store, err := authority.Identity.Repository()
	if err != nil {
		return WorkspaceQuotaStatus{}, quotaStatusError(err)
	}
	if store.Dialect() != transaction.PostgreSQL {
		return WorkspaceQuotaStatus{}, ErrReservationTransaction
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(authority.Identity.Config.Policy().HTTP.RequestSeconds))
	defer cancel()

	var out WorkspaceQuotaStatus
	err = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		scope, err := authority.ReadScopeWithin(ctx, q, actor, workspace)
		if err != nil {
			return err
		}
		out, err = readWorkspaceQuotaStatusWithin(ctx, q, scope.TenantID, scope.ID)
		return err
	})
	if err != nil {
		return WorkspaceQuotaStatus{}, quotaStatusError(err)
	}
	return out, nil
}

func readWorkspaceQuotaStatusWithin(ctx context.Context, q transaction.Executor, tenant, workspace string) (WorkspaceQuotaStatus, error) {
	out := WorkspaceQuotaStatus{WorkspaceID: workspace}
	revision, err := lockReservationPolicy(ctx, q)
	if errors.Is(err, ErrQuotaUnconfigured) {
		out.State = WorkspaceQuotaUnconfigured
		out.Reason = &WorkspaceQuotaReason{Code: ErrQuotaUnconfigured.Error(), Action: ConfigureQuotaPolicy}
		return out, nil
	}
	if err != nil {
		return WorkspaceQuotaStatus{}, err
	}
	if err := lockReservationWorkspace(ctx, q, tenant, workspace); err != nil {
		return WorkspaceQuotaStatus{}, err
	}
	readRevision, tree, err := readQuotaPolicyWithin(ctx, q)
	if err != nil || tree == nil || revision != readRevision {
		return WorkspaceQuotaStatus{}, ErrQuotaPolicyUnavailable
	}
	used, err := readWorkspaceUsageWithin(ctx, q, tenant, workspace)
	if err != nil {
		return WorkspaceQuotaStatus{}, err
	}
	out.PolicyRevision = revision
	node, err := tree.workspace(tenant, workspace)
	if errors.Is(err, ErrQuotaScopeNotFound) && len(used) == 0 {
		out.State = WorkspaceQuotaUnallocated
		out.Reason = &WorkspaceQuotaReason{Code: "SCHEDULING_QUOTA_UNALLOCATED", Action: ConfigureWorkspaceQuota}
		return out, nil
	}
	if err != nil {
		return WorkspaceQuotaStatus{}, ErrQuotaPolicyUnavailable
	}
	out.Resources, err = workspaceQuotaResources(node.Limits, used)
	if err != nil {
		return WorkspaceQuotaStatus{}, err
	}
	out.State = WorkspaceQuotaConfigured
	return out, nil
}

func workspaceQuotaResources(quota Quota, used Resources) ([]WorkspaceQuotaResource, error) {
	reserved, err := normalizeResources(used)
	if err != nil || !reserved.fits(quota.Burst) {
		return nil, ErrQuotaPolicyUnavailable
	}
	keys := make([]Resource, 0, len(quota.Burst))
	for resource := range quota.Burst {
		keys = append(keys, resource)
	}
	slices.SortFunc(keys, func(a, b Resource) int { return strings.Compare(string(a), string(b)) })
	out := make([]WorkspaceQuotaResource, 0, len(keys))
	for _, resource := range keys {
		limit, held := quota.Burst[resource], reserved[resource]
		out = append(out, WorkspaceQuotaResource{
			Resource: resource, Guaranteed: strconv.FormatInt(quota.Guaranteed[resource], 10),
			HardLimit: strconv.FormatInt(limit, 10), Reserved: strconv.FormatInt(held, 10),
			UnreservedQuota: strconv.FormatInt(limit-held, 10),
		})
	}
	return out, nil
}

func quotaStatusError(err error) error {
	if kind, ok := transaction.CommitOutcome(err); ok {
		return &transaction.Error{Kind: kind}
	}
	var storage *transaction.Error
	if errors.As(err, &storage) {
		switch storage.Kind {
		case transaction.Cancelled, transaction.Timeout, transaction.LockTimeout, transaction.Deadlock:
			return &transaction.Error{Kind: storage.Kind}
		default:
			return &transaction.Error{Kind: transaction.Unavailable}
		}
	}
	switch {
	case errors.Is(err, context.Canceled):
		return &transaction.Error{Kind: transaction.Cancelled}
	case errors.Is(err, context.DeadlineExceeded):
		return &transaction.Error{Kind: transaction.Timeout}
	}
	switch err.Error() {
	case "RESOURCE_NOT_FOUND", "ACCESS_DENIED", "AUTH_SESSION_EXPIRED",
		"AUTH_ACCOUNT_DISABLED", "AUTH_SOURCE_DISABLED", "AUTH_REAUTHENTICATION_REQUIRED":
		return errors.New(err.Error())
	default:
		return ErrQuotaPolicyUnavailable
	}
}
