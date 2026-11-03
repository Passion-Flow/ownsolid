package engine

const StoreRestoreConfigRole = "restore-control-config"
const StoreRestoreArchiveRole = "restore-files"
const StoreRestoreSourceDestination = "/var/lib/projectone/files-restore"

const ModelFilesImport = "import-model-files"
const DatasetFilesImport = "import-dataset-files"
const TrainingFilesImport = "import-training-files"
const PreparationFilesImport = "import-preparation-files"
const ModelFilesVerify = "verify-model-store"
const DatasetFilesVerify = "verify-dataset-store"
const TrainingFilesVerify = "verify-training-files"
const PreparationFilesVerify = "verify-preparation-files"

type storeRestoreTaskProfile struct {
	role                string
	importFiles, reason bool
}

func storeRestoreTask(kind string) (storeRestoreTaskProfile, bool) {
	profiles := map[string]storeRestoreTaskProfile{
		ModelFilesImport: {"models", true, true}, DatasetFilesImport: {"datasets", true, true}, TrainingFilesImport: {"training", true, true}, PreparationFilesImport: {"preparations", true, true},
		ModelFilesVerify: {"models", false, false}, DatasetFilesVerify: {"datasets", false, false}, TrainingFilesVerify: {"training", false, true}, PreparationFilesVerify: {"preparations", false, true},
	}
	p, ok := profiles[kind]
	return p, ok
}

func (t DatabaseTask) storeRestoreArguments() []string {
	p, ok := storeRestoreTask(t.Kind)
	if !ok {
		return nil
	}
	args := []string{}
	if p.importFiles {
		args = append(args, "--backup-root", StoreRestoreSourceDestination+"/"+storeSnapshotRoot)
		confirmation := ""
		if t.Kind == TrainingFilesImport {
			confirmation = "training"
		} else if t.Kind == PreparationFilesImport {
			confirmation = "preparation"
		}
		if confirmation != "" {
			destination, _ := t.outputProfile()
			args = append(args, "--"+confirmation+"-store-root", destination+"/"+UtilityPrivateDirectory, "--confirm-empty-"+confirmation+"-store")
		}
	}
	if p.reason {
		args = append(args, "--reason", t.Reason)
	}
	return args
}

func (t DatabaseTask) validStoreRestoreTask(p storeRestoreTaskProfile) bool {
	if t.VolumeOwner.OperationID != t.Owner.OperationID || t.OutputOwner == nil || !t.OutputOwner.valid() || t.OutputOwner.ScopeID != t.Owner.ScopeID || t.OutputOwner.Role != p.role || t.Peers["control"] == "" || p.reason && !validStoreReason(t.Reason) || !p.reason && t.Reason != "" {
		return false
	}
	if !p.importFiles {
		return t.StoreRestore == nil && t.SourceOwner == nil
	}
	if t.StoreRestore == nil || !t.StoreRestore.valid() || t.SourceOwner == nil || !t.SourceOwner.valid() || t.SourceOwner.ScopeID != t.Owner.ScopeID || t.SourceOwner.OperationID != t.Owner.OperationID || t.SourceOwner.Role != StoreRestoreArchiveRole+"-"+p.role+"-"+t.Owner.OperationID {
		return false
	}
	r := t.StoreRestore.Artifact
	return r.Role == p.role && r.UID == t.UID && r.GID == t.GID
}

// ValidateRestoredStoreTask checks only the fixed import/verifier profile. It
// performs no Engine access and admits no fresh initialization or arbitrary job.
func (t DatabaseTask) ValidateRestoredStoreTask() error {
	if _, ok := storeRestoreTask(t.Kind); !ok || !t.valid() {
		return failure("OFFLINE_ENGINE_RESTORED_STORE_TASK_INVALID")
	}
	return nil
}

// RestoredStoreDestination exposes the fixed container protocol path so scope
// admission can compare it with the serving mount without duplicating paths.
func (t DatabaseTask) RestoredStoreDestination() (string, error) {
	if e := t.ValidateRestoredStoreTask(); e != nil {
		return "", e
	}
	destination, _ := t.outputProfile()
	return destination, nil
}
