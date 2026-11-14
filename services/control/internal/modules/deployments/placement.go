package deployments

import (
	"context"
	"fmt"
	"sort"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Placement is where a model can be deployed now, judged by the checks a deployment itself
// makes. It reserves nothing: the deployment checks again when it is created.
type Placement struct {
	ModelID     string           `json:"model_id"`
	WorkspaceID string           `json:"workspace_id"`
	Candidates  []PlacementNode  `json:"candidates"`
	Recommended *PlacementChoice `json:"recommended"`
}

// PlacementNode is one node that has reported its GPUs, with the reason none of them can take
// the model when the reason belongs to the node as a whole.
type PlacementNode struct {
	NodeID     string         `json:"node_id"`
	Name       string         `json:"name"`
	ObservedAt time.Time      `json:"observed_at"`
	Refusal    *string        `json:"refusal"`
	GPUs       []PlacementGPU `json:"gpus"`
}

// PlacementGPU is one GPU as its node last reported it, with the reason it cannot take the
// model, if there is one.
type PlacementGPU struct {
	UUID           string  `json:"uuid"`
	Name           string  `json:"name"`
	MemoryTotalMiB int64   `json:"memory_total_mib"`
	MemoryFreeMiB  int64   `json:"memory_free_mib"`
	Refusal        *string `json:"refusal"`
}

// PlacementChoice is the GPU a deployment would take if the person accepts the recommendation.
type PlacementChoice struct {
	NodeID  string `json:"node_id"`
	GPUUUID string `json:"gpu_uuid"`
}

type placementRow struct {
	node     string
	raw      []byte
	fresh    bool
	retained bool
}

// Placement reads the nodes of a model's workspace that most recently reported their GPUs, at
// most one lookup page of them, and judges every GPU for the model. A deployment takes a whole
// GPU and a node serves one deployment, so the recommendation is the eligible GPU with the most
// free memory, the most recently observed node winning a tie.
func (s *Service) Placement(ctx context.Context, a identity.Auth, model string) (Placement, error) {
	if !id(model) {
		return Placement{}, fail("VALIDATION_FAILED")
	}
	if a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin {
		return Placement{}, fail("ACCESS_DENIED")
	}
	if _, e := s.runtimeRepository(); e != nil {
		return Placement{}, e
	}
	version, e := s.Models.Get(ctx, a, "", model)
	if e != nil {
		return Placement{}, e
	}
	if e = s.admitModelUse(ctx, version.WorkspaceID, version.Kind, version.CatalogDigest); e != nil {
		return Placement{}, e
	}
	derived := version.Kind == wire.DerivedModelKind
	if !derived && version.Kind != "approved_base" {
		return Placement{}, fail("MODEL_FILE_INVALID")
	}
	out := Placement{ModelID: version.ID, WorkspaceID: version.WorkspaceID, Candidates: []PlacementNode{}}
	e = s.runtimeActor(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		w, e := s.Nodes.Workspaces.ReadScopeWithin(ctx, q, a, version.WorkspaceID)
		if e != nil {
			return e
		}
		rows, e := s.placementRows(ctx, q, w.TenantID, w.ID)
		if e != nil {
			return e
		}
		window := s.gpuObservationWindow()
		for _, row := range rows {
			n, e := s.Nodes.LookupWithin(ctx, q, w.TenantID, w.ID, row.node)
			if e != nil {
				return e
			}
			var o wire.InferenceObservation
			if e := runtimeDecodeObservation(q, row.raw, &o); e != nil {
				return e
			}
			out.Candidates = append(out.Candidates, judgeNode(n, o, row, derived, window))
		}
		return nil
	})
	if e != nil {
		return Placement{}, e
	}
	out.Recommended = recommend(out.Candidates)
	return out, nil
}

func (s *Service) placementRows(ctx context.Context, q transaction.Executor, tenant, workspace string) ([]placementRow, error) {
	age := s.Nodes.Identity.Config.Policy().Nodes.OnlineSeconds
	limit := s.Nodes.Identity.Config.Policy().UI.LookupPageSize
	statement := placementStatement(q.Dialect(), tenant, workspace, age, limit)
	rows, e := q.Query(ctx, statement)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []placementRow{}
	for rows.Next() {
		var row placementRow
		if e := rows.Scan(&row.node, &row.raw, &row.fresh, &row.retained); e != nil {
			return nil, e
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// placementStatement reads each observed node of a workspace with the freshness of its
// observation and whether it already serves a deployment. Only the validated integers become
// statement text.
func placementStatement(dialect transaction.Dialect, tenant, workspace string, age, limit int) transaction.Statement {
	if age < 1 || limit < 1 || limit > 100 {
		return transaction.SQL(dialect, "")
	}
	switch dialect {
	case transaction.PostgreSQL:
		return transaction.SQL(dialect, `SELECT i.node_id::text,i.observation,i.received_at>clock_timestamp()-make_interval(secs=>$3),EXISTS(SELECT 1 FROM deployments d WHERE d.tenant_id=i.tenant_id AND d.workspace_id=i.workspace_id AND d.node_id=i.node_id AND d.removed_at IS NULL) FROM inference_nodes i WHERE i.tenant_id=$1 AND i.workspace_id=$2 ORDER BY i.received_at DESC,i.node_id LIMIT $4`, tenant, workspace, age, limit)
	case transaction.MySQL:
		return transaction.SQL(dialect, `SELECT i.node_id,i.observation,i.received_at>TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)),EXISTS(SELECT 1 FROM deployments d WHERE d.tenant_id=i.tenant_id AND d.workspace_id=i.workspace_id AND d.node_id=i.node_id AND d.removed_at IS NULL) FROM inference_nodes i WHERE i.tenant_id=? AND i.workspace_id=? ORDER BY i.received_at DESC,i.node_id LIMIT ?`, -age, tenant, workspace, limit)
	case transaction.Dameng:
		return transaction.SQL(dialect, fmt.Sprintf(`SELECT I."NODE_ID",I."OBSERVATION",CASE WHEN I."RECEIVED_AT">TIMESTAMPADD(SECOND,-?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))) THEN 1 ELSE 0 END,CASE WHEN EXISTS(SELECT 1 FROM "DEPLOYMENTS" D WHERE D."TENANT_ID"=I."TENANT_ID" AND D."WORKSPACE_ID"=I."WORKSPACE_ID" AND D."NODE_ID"=I."NODE_ID" AND D."REMOVED_AT" IS NULL) THEN 1 ELSE 0 END FROM "INFERENCE_NODES" I WHERE I."TENANT_ID"=? AND I."WORKSPACE_ID"=? ORDER BY I."RECEIVED_AT" DESC,I."NODE_ID" LIMIT %d`, limit), age, tenant, workspace)
	}
	return transaction.SQL(dialect, "")
}

// judgeNode applies, in the order a deployment applies them, the checks a new deployment of
// the model on this node would meet.
func judgeNode(n nodes.Node, o wire.InferenceObservation, row placementRow, derived bool, window gpuObservationWindow) PlacementNode {
	out := PlacementNode{NodeID: n.ID, Name: n.Name, ObservedAt: o.ObservedAt.UTC(), GPUs: []PlacementGPU{}}
	refuse := func(e error) *string {
		if e == nil {
			return nil
		}
		code := e.Error()
		return &code
	}
	switch {
	case derived && !derivedProfile(n):
		out.Refusal = refuse(fail("AGENT_PROFILE_REJECTED"))
	case admitNode(n) != nil:
		out.Refusal = refuse(admitNode(n))
	case !row.fresh:
		out.Refusal = refuse(fail("INFERENCE_PREFLIGHT_REQUIRED"))
	case admitObservation(o, wire.InferenceCommand{Action: "deploy"}) != nil:
		out.Refusal = refuse(admitObservation(o, wire.InferenceCommand{Action: "deploy"}))
	case !window.contains(o.ObservedAt):
		out.Refusal = refuse(fail("INFERENCE_PREFLIGHT_REQUIRED"))
	case row.retained:
		out.Refusal = refuse(fail("INFERENCE_RESOURCE_BUSY"))
	}
	for _, g := range o.GPUs {
		gpu := PlacementGPU{UUID: g.UUID, Name: g.Name, MemoryTotalMiB: g.MemoryTotalMiB, MemoryFreeMiB: g.MemoryFreeMiB}
		if out.Refusal == nil {
			gpu.Refusal = refuse(admitGPU(o, g.UUID, derived, true, window))
		}
		out.GPUs = append(out.GPUs, gpu)
	}
	return out
}

func recommend(candidates []PlacementNode) *PlacementChoice {
	type option struct {
		order int
		node  string
		gpu   PlacementGPU
	}
	options := []option{}
	for order, n := range candidates {
		if n.Refusal != nil {
			continue
		}
		for _, g := range n.GPUs {
			if g.Refusal == nil {
				options = append(options, option{order, n.NodeID, g})
			}
		}
	}
	if len(options) == 0 {
		return nil
	}
	sort.SliceStable(options, func(i, j int) bool {
		if options[i].gpu.MemoryFreeMiB != options[j].gpu.MemoryFreeMiB {
			return options[i].gpu.MemoryFreeMiB > options[j].gpu.MemoryFreeMiB
		}
		return options[i].order < options[j].order
	})
	return &PlacementChoice{NodeID: options[0].node, GPUUUID: options[0].gpu.UUID}
}
