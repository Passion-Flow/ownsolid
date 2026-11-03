package engine

import (
	"context"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const DatasetStoreInitialize = "initialize-dataset-store"
const DatasetStoreCheck = "check-dataset-store-initialization"
const DatasetStoreRecord = "record-dataset-store-initialization"
const TrainingStoreInitialize = "initialize-training-store"
const TrainingStoreCheck = "check-training-store-initialization"
const TrainingStoreRecord = "record-training-store-initialization"
const PreparationStoreInitialize = "initialize-preparation-store"
const PreparationStoreCheck = "check-preparation-store-initialization"
const PreparationStoreRecord = "record-preparation-store-initialization"

type storeTaskProfile struct {
	role, kind, mode string
	children         []string
}

func storeTask(kind string) (storeTaskProfile, bool) {
	p := storeTaskProfile{}
	switch kind {
	case DatasetStoreInitialize, DatasetStoreCheck, DatasetStoreRecord:
		p = storeTaskProfile{role: "datasets", kind: "dataset", children: []string{"objects", "attempts", "versions"}}
	case TrainingStoreInitialize, TrainingStoreCheck, TrainingStoreRecord:
		p = storeTaskProfile{role: "training", kind: "training", children: []string{"uploads", "checkpoints", "artifacts"}}
	case PreparationStoreInitialize, PreparationStoreCheck, PreparationStoreRecord:
		p = storeTaskProfile{role: "preparations", kind: "preparation", children: []string{"uploads", "artifacts"}}
	default:
		return p, false
	}
	if strings.HasPrefix(kind, "initialize-") {
		p.mode = "initialize"
	} else if strings.HasPrefix(kind, "check-") {
		p.mode = "check"
	} else {
		p.mode = "record"
	}
	return p, true
}
func (p storeTaskProfile) destination() string { return "/var/lib/projectone/" + p.role }

const maximumStoreReasonRunes = 500

func validStoreReason(reason string) bool {
	return utf8.ValidString(reason) && strings.TrimSpace(reason) != "" && utf8.RuneCountInString(reason) <= maximumStoreReasonRunes && !strings.ContainsFunc(reason, unicode.IsControl)
}
func (t DatabaseTask) outputProfile() (string, bool) {
	if p, ok := storeRestoreTask(t.Kind); ok {
		return "/var/lib/projectone/" + p.role, false
	}
	if _, ok := storeBackupTask(t.Kind); ok {
		return StoreBackupDestination, false
	}
	if t.bootstrap() {
		return BootstrapTaskDestination, t.Kind == DatabaseBootstrapReconcile
	}
	if p, ok := storeTask(t.Kind); ok {
		return p.destination(), p.mode == "check"
	}
	return "", false
}
func (t DatabaseTask) storeArguments() []string {
	p, ok := storeTask(t.Kind)
	if !ok {
		return nil
	}
	result := []string{"--" + p.kind + "-store-root", p.destination() + "/private", "--owner-uid", strconv.Itoa(t.UID), "--owner-gid", strconv.Itoa(t.GID)}
	if p.mode == "initialize" {
		result = append(result, "--confirm-empty-"+p.kind+"-store")
	}
	if p.mode != "check" {
		result = append(result, "--reason", t.Reason)
	}
	return result
}

// Only the recorded exact control peer may share an initialized data volume.
// Operator inputs and bootstrap credentials remain exclusive to the task.
func (c *Client) databaseOutputConsumers(ctx context.Context, t DatabaseTask, id string) error {
	if t.SourceOwner != nil {
		allowed := []string{t.Peers["control"]}
		if p, ok := storeRestoreTask(t.Kind); ok && p.importFiles {
			allowed = []string{}
		}
		if id != "" {
			allowed = append(allowed, id)
		}
		if e := c.VerifyVolumeConsumers(ctx, *t.SourceOwner, allowed); e != nil {
			return e
		}
	}
	if t.OutputOwner == nil {
		return nil
	}
	allowed := []string{}
	if id != "" {
		allowed = append(allowed, id)
	}
	if _, ok := storeTask(t.Kind); ok {
		allowed = append(allowed, t.Peers["control"])
	}
	if _, ok := storeRestoreTask(t.Kind); ok {
		allowed = append(allowed, t.Peers["control"])
	}
	return c.VerifyVolumeConsumers(ctx, *t.OutputOwner, allowed)
}

// StoreTaskContext exposes only the immutable fixed command mapping to the
// scope journal, which must not maintain a second command/profile authority.
func StoreTaskContext(kind string) (role, mode string, ok bool) {
	p, ok := storeTask(kind)
	return p.role, p.mode, ok
}
func ValidStoreTaskReason(reason string) bool { return validStoreReason(reason) }
