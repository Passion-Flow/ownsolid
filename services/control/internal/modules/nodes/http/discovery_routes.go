package http

import "strings"

// DiscoveryHumanOwns is the closed M03 subset of the cumulative node API.
func DiscoveryHumanOwns(path string) bool {
	p := strings.Split(strings.TrimPrefix(path, "/api/v1/"), "/")
	if len(p) < 2 {
		return false
	}
	if p[0] == "admin" {
		if p[1] == "node-join-tokens" {
			return len(p) == 2 || len(p) == 4 && p[3] == "revocations"
		}
		return p[1] == "nodes" && (len(p) == 2 || len(p) == 3 || len(p) == 4 && p[3] == "tasks")
	}
	return p[0] == "workspaces" && len(p) >= 3 && p[2] == "nodes" && (len(p) == 3 || len(p) == 4 || len(p) == 5 && p[4] == "tasks")
}
// ClaimRoute reports the claims path, which may long-poll for up to MaxClaimWaitSeconds.
func ClaimRoute(path, method string) bool {
	p := strings.Split(strings.TrimPrefix(path, "/agent/v1/nodes/"), "/")
	return method == "POST" && strings.HasPrefix(path, "/agent/v1/nodes/") && len(p) == 3 && p[1] == "tasks" && p[2] == "claims"
}

func DiscoveryMachineOwns(path string) bool {
	if path == "/agent/v1/enroll" {
		return true
	}
	if !strings.HasPrefix(path, "/agent/v1/nodes/") {
		return false
	}
	p := strings.Split(strings.TrimPrefix(path, "/agent/v1/nodes/"), "/")
	return len(p) == 2 && p[1] == "heartbeat" || len(p) == 3 && p[1] == "tasks" && p[2] == "claims" || len(p) == 4 && p[1] == "tasks" && p[3] == "results"
}

// ComponentsHumanOwns adds only M04 state and Admin actions to discovery.
func ComponentsHumanOwns(path string) bool {
	if DiscoveryHumanOwns(path) {
		return true
	}
	p := strings.Split(strings.TrimPrefix(path, "/api/v1/"), "/")
	if len(p) >= 6 && p[0] == "admin" && p[1] == "nodes" && p[3] == "components" && p[4] == "node-exporter" {
		return len(p) == 6 && p[5] == "actions"
	}
	if len(p) == 5 && p[0] == "admin" && p[1] == "nodes" && p[3] == "components" && p[4] == "node-exporter" {
		return true
	}
	return len(p) == 6 && p[0] == "workspaces" && p[2] == "nodes" && p[4] == "components" && p[5] == "node-exporter"
}
func ComponentsMachineOwns(path string) bool {
	if DiscoveryMachineOwns(path) {
		return true
	}
	if !strings.HasPrefix(path, "/agent/v1/nodes/") {
		return false
	}
	p := strings.Split(strings.TrimPrefix(path, "/agent/v1/nodes/"), "/")
	return len(p) == 4 && p[1] == "tasks" && (p[3] == "renewals" || p[3] == "artifact")
}

// MaintenanceHumanOwns adds only the separate Admin log snapshot to M04 routes.
func MaintenanceHumanOwns(path string) bool {
	if ComponentsHumanOwns(path) {
		return true
	}
	p := strings.Split(strings.TrimPrefix(path, "/api/v1/"), "/")
	return len(p) == 6 && p[0] == "admin" && p[1] == "nodes" && p[3] == "components" && p[4] == "node-exporter" && p[5] == "logs"
}

// ModelsMachineOwns adds only immutable approved-model transfer to existing tasks.
func ModelsMachineOwns(path string) bool {
	if ComponentsMachineOwns(path) {
		return true
	}
	if !strings.HasPrefix(path, "/agent/v1/nodes/") {
		return false
	}
	p := strings.Split(strings.TrimPrefix(path, "/agent/v1/nodes/"), "/")
	return len(p) == 4 && p[1] == "tasks" && (p[3] == "model-manifest" || p[3] == "model-files")
}

// TrainingMachineOwns adds only current M09 transfers and positive physical stop.
func TrainingMachineOwns(path string) bool {
	if ModelsMachineOwns(path) {
		return true
	}
	if !strings.HasPrefix(path, "/agent/v1/nodes/") {
		return false
	}
	p := strings.Split(strings.TrimPrefix(path, "/agent/v1/nodes/"), "/")
	if len(p) < 4 || !id(p[0]) || p[1] != "tasks" || !id(p[2]) {
		return false
	}
	if len(p) == 4 {
		return p[3] == "training-input-files" || p[3] == "training-events" || p[3] == "training-stops" || p[3] == "training-uploads"
	}
	return len(p) == 6 && p[3] == "training-uploads" && id(p[4]) && (p[5] == "chunks" || p[5] == "finish")
}

// LifecycleMachineOwns admits only the negotiated M10 file/event/output/stop leaves.
func LifecycleMachineOwns(path string) bool {
	if TrainingMachineOwns(path) {
		return true
	}
	const prefix = "/agent/v1/nodes/"
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	p := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(p) < 4 || !id(p[0]) || p[1] != "tasks" || !id(p[2]) {
		return false
	}
	if len(p) == 4 {
		return p[3] == "model-preparation-input-files" || p[3] == "model-preparation-events" || p[3] == "model-preparation-stops" || p[3] == "model-preparation-uploads"
	}
	return len(p) == 6 && p[3] == "model-preparation-uploads" && id(p[4]) && (p[5] == "chunks" || p[5] == "finish")
}
