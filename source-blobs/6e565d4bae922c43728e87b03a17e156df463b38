package components

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"strings"
	"unicode/utf8"
)

type recorded struct {
	version  string
	applied  *wire.ComponentConfig
	previous *string
}

func (s *Service) recordedWithin(ctx context.Context, q transaction.Executor, n nodes.Node) (recorded, error) {
	out := recorded{version: wire.NodeExporterVersion}
	var raw []byte
	e := q.QueryRow(ctx, componentSQL(q, "SELECT version,applied_config,previous_version FROM managed_components WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND component='node_exporter'", "SELECT version,applied_config,previous_version FROM managed_components WHERE tenant_id=? AND workspace_id=? AND node_id=? AND component='node_exporter'", `SELECT version,applied_config,previous_version FROM managed_components WHERE tenant_id=? AND workspace_id=? AND node_id=? AND component='node_exporter'`, n.TenantID, n.WorkspaceID, n.ID)).Scan(&out.version, &raw, &out.previous)
	if errors.Is(e, sql.ErrNoRows) {
		return out, nil
	}
	if e != nil {
		return out, e
	}
	out.applied, e = decodeConfig(raw)
	if e != nil {
		return out, e
	}
	if _, ok := wire.RecipeForVersion(out.version); !ok {
		return out, errors.New("STORAGE_UNAVAILABLE")
	}
	return out, nil
}
func (s *Service) Request(ctx context.Context, a identity.Auth, node, action, key, reason, request string) (nodes.Task, error) {
	return s.RequestDetailed(ctx, a, node, Request{Action: action, Reason: reason}, key, request)
}
func (s *Service) RequestDetailed(ctx context.Context, a identity.Auth, node string, input Request, key, request string) (nodes.Task, error) {
	if a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin {
		return nodes.Task{}, errors.New("ACCESS_DENIED")
	}
	if !utf8.ValidString(input.Reason) || strings.TrimSpace(input.Reason) == "" || utf8.RuneCountInString(input.Reason) > 500 || !wire.IsComponentOperation(wire.ComponentOperation(input.Action)) {
		return nodes.Task{}, errors.New("VALIDATION_FAILED")
	}
	if _, e := s.repository(); e != nil {
		return nodes.Task{}, e
	}
	if input.Config != nil {
		copy := *input.Config
		input.Config = &copy
		if e := input.Config.Validate(); e != nil {
			return nodes.Task{}, e
		}
	}
	if input.Version != "" {
		if _, ok := wire.RecipeForVersion(input.Version); !ok {
			return nodes.Task{}, errors.New("COMPONENT_VERSION_UNSUPPORTED")
		}
	}
	n, e := s.Nodes.Get(ctx, a, "", node)
	if e != nil {
		return nodes.Task{}, e
	}
	if !wire.IsManagementProfile(n.ExecutionProfile) {
		return nodes.Task{}, errors.New("COMPONENT_PROFILE_UNAVAILABLE")
	}
	legacy := n.ExecutionProfile != wire.SystemdMaintenanceProfile
	if legacy {
		command := wire.Command(input.Action)
		if input.Version != "" || input.Config != nil || command.Validate(wire.ComponentOperation(input.Action)) != nil {
			return nodes.Task{}, errors.New("COMPONENT_MAINTENANCE_REQUIRED")
		}
	}

	if input.Config != nil && input.Action != "install" && input.Action != "configure" {
		return nodes.Task{}, errors.New("COMPONENT_CONFIG_INVALID")
	}
	if input.Version != "" && input.Action != "install" && input.Action != "configure" && input.Action != "upgrade" && input.Action != "rollback" {
		return nodes.Task{}, errors.New("VALIDATION_FAILED")
	}
	if input.Action == "configure" && input.Config == nil {
		return nodes.Task{}, errors.New("COMPONENT_CONFIG_INVALID")
	}
	if input.Action == "upgrade" && input.Version == "" {
		return nodes.Task{}, errors.New("COMPONENT_VERSION_UNSUPPORTED")
	}
	intent, _ := json.Marshal(input)
	fingerprint := security.Digest("component-maintenance-request:v1:" + string(intent))
	if legacy {
		raw, _ := json.Marshal(wire.Command(input.Action))
		fingerprint = security.Digest("component-intent:v1:" + string(raw))
	}
	// Keep verified descriptors outside the authority transaction. Missing assets
	// are checked only by a new command builder, after exact replay resolution.
	files := map[string]*os.File{}
	assetErrors := map[string]error{}
	infos := map[string]os.FileInfo{}
	if input.Action == "install" || input.Action == "upgrade" {
		for _, v := range []string{"1.12.0", wire.NodeExporterVersion} {
			if f, _, e := s.OpenArtifactVersionContext(ctx, v); e == nil {
				files[v] = f
				infos[v], _ = f.Stat()
			} else {
				assetErrors[v] = e
			}
		}
	}
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()
	var out nodes.Task
	e = s.within(ctx, a, "", node, func(ctx context.Context, q transaction.Executor, _ nodes.Node) error {
		var e error
		out, e = s.Nodes.CreateComponentTaskBoundWithin(ctx, q, a, node, wire.ComponentOperation(input.Action), key, input.Reason, request, fingerprint, func(ctx context.Context, q transaction.Executor, n nodes.Node) (wire.ComponentCommand, error) {
			var command wire.ComponentCommand
			if n.ExecutionProfile != wire.SystemdMaintenanceProfile {
				if !legacy {
					return command, errors.New("COMPONENT_PROFILE_UNAVAILABLE")
				}
				command = wire.Command(input.Action)
			} else {
				if legacy {
					return command, errors.New("COMPONENT_PROFILE_UNAVAILABLE")
				}
				var e error
				command, e = s.buildMaintenanceWithin(ctx, q, n, input)
				if e != nil {
					return wire.ComponentCommand{}, e
				}
			}
			if input.Action == "install" || input.Action == "upgrade" {
				f := files[command.Version]
				if f == nil {
					if e := assetErrors[command.Version]; e != nil {
						return wire.ComponentCommand{}, e
					}
					return wire.ComponentCommand{}, errors.New("COMPONENT_ASSET_UNAVAILABLE")
				}
				if e := s.checkArtifactDescriptor(f, infos[command.Version], command.Version); e != nil {
					return wire.ComponentCommand{}, e
				}
			}
			return command, nil
		}, s.recordRequestWithin)
		return e
	})
	if e != nil {
		return nodes.Task{}, e
	}
	return out, nil
}
func (s *Service) buildMaintenanceWithin(ctx context.Context, q transaction.Executor, n nodes.Node, input Request) (wire.ComponentCommand, error) {

	current, e := s.recordedWithin(ctx, q, n)
	if e != nil {
		return wire.ComponentCommand{}, e
	}
	version := current.version
	expected := current.version
	if current.applied == nil {
		expected = ""
	}
	configuration := input.Config
	switch input.Action {
	case "install":
		if input.Version != "" {
			version = input.Version
		} else if current.applied == nil {
			version = wire.NodeExporterVersion
		}
		if current.applied != nil && version != current.version {
			return wire.ComponentCommand{}, errors.New("COMPONENT_VERSION_CONFLICT")
		}
		if configuration == nil {
			configuration = current.applied
			if configuration == nil {
				value := wire.DefaultComponentConfig()
				configuration = &value
			}
		}
	case "configure":
		if current.applied == nil {
			return wire.ComponentCommand{}, errors.New("COMPONENT_STATE_INVALID")
		}
		if input.Version != "" && input.Version != version {
			return wire.ComponentCommand{}, errors.New("COMPONENT_VERSION_CONFLICT")
		}
	case "upgrade":
		if current.applied == nil {
			return wire.ComponentCommand{}, errors.New("COMPONENT_STATE_INVALID")
		}
		version = input.Version
		configuration = current.applied
	case "rollback":
		if current.previous == nil || input.Version != "" && input.Version != *current.previous {
			return wire.ComponentCommand{}, errors.New("COMPONENT_ROLLBACK_UNAVAILABLE")
		}
		version = *current.previous
	case "observe", "logs":
		expected = ""
	}
	command, e := wire.MaintenanceCommand(input.Action, version, expected, configuration)
	if e != nil {
		return command, e
	}
	return command, nil
}

func (s *Service) recordRequestWithin(ctx context.Context, q transaction.Executor, n nodes.Node, t nodes.Task, command wire.ComponentCommand) error {
	desired := "running"
	preserveState := false
	switch command.Action {
	case "stop":
		desired = "stopped"
	case "remove":
		desired = "not_installed"
	case "observe", "logs":
		desired = "not_installed"
		preserveState = true
	}
	configuration := command.Config
	replaceConfig := command.Action == "install" || command.Action == "configure" || command.Action == "upgrade" || command.Action == "rollback"
	if configuration == nil && command.Action != "rollback" {
		value := wire.DefaultComponentConfig()
		configuration = &value
	}
	if q.Dialect() == transaction.Dameng {
		return s.requestNativeWithin(ctx, q, n, t, command, desired, configuration, preserveState, replaceConfig)
	}
	_, e := q.Exec(ctx, componentSQL(q, `INSERT INTO managed_components(tenant_id,workspace_id,node_id,component,version,desired_state,observed_state,managed,observation,observed_at,last_task_id,desired_version,desired_config)
 VALUES($1,$2,$3,'node_exporter',$4,$5,'unknown',false,'{}',clock_timestamp(),$6,$4,$7)
 ON CONFLICT(node_id,component) DO UPDATE SET desired_state=CASE WHEN $8 THEN managed_components.desired_state ELSE EXCLUDED.desired_state END,last_task_id=EXCLUDED.last_task_id,desired_version=CASE WHEN $8 THEN managed_components.desired_version ELSE EXCLUDED.desired_version END,desired_config=CASE WHEN $9 THEN EXCLUDED.desired_config ELSE managed_components.desired_config END`, `INSERT INTO managed_components(tenant_id,workspace_id,node_id,component,version,desired_state,observed_state,managed,observation,observed_at,last_task_id,desired_version,desired_config)
 VALUES(?,?,?,'node_exporter',?,?,'unknown',false,JSON_OBJECT(),UTC_TIMESTAMP(6),?,?,CAST(? AS JSON)) AS incoming
 ON DUPLICATE KEY UPDATE desired_state=CASE WHEN ? THEN managed_components.desired_state ELSE incoming.desired_state END,last_task_id=incoming.last_task_id,desired_version=CASE WHEN ? THEN managed_components.desired_version ELSE incoming.desired_version END,desired_config=CASE WHEN ? THEN incoming.desired_config ELSE managed_components.desired_config END`, "", requestArgs(q, n, t, command, desired, configuration, preserveState, replaceConfig)...))
	return e
}
func requestArgs(q transaction.Executor, n nodes.Node, t nodes.Task, c wire.ComponentCommand, desired string, configuration *wire.ComponentConfig, preserve, replace bool) []any {
	if q.Dialect() == transaction.MySQL {
		return []any{n.TenantID, n.WorkspaceID, n.ID, c.Version, desired, t.ID, c.Version, encodeConfig(configuration), preserve, preserve, replace}
	}
	return []any{n.TenantID, n.WorkspaceID, n.ID, c.Version, desired, t.ID, encodeConfig(configuration), preserve, replace}
}
