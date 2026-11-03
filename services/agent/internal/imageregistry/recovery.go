package imageregistry

import (
	"context"
	"errors"
	"os"
	"reflect"
	"time"

	"github.com/Passion-Flow/ownsolid/services/agent/internal/inference"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

// A new grant may renew authority for the same immutable intent. It cannot
// change its subject, proof or image bytes, or authorize another revision.
func sameTaskImageIntent(original, current wire.RegistryManifest) bool {
	return original.DeploymentID == current.DeploymentID && original.Revision == current.Revision &&
		reflect.DeepEqual(original.ImageIdentity(), current.ImageIdentity())
}

func (l *Loader) taskIntent(root *os.Root, job string) (wire.RegistryManifest, error) {
	var m wire.RegistryManifest
	if privateJob(root, job) != nil || readPrivateJSON(root, job+"/intent.json", l.config.Verification.MaximumMetadataBytes, &m) != nil || m.ImageIdentity().Validate() != nil {
		return m, errors.New("REGISTRY_INTENT_STALE")
	}
	return m, nil
}

func (l *Loader) reconcileImage(ctx context.Context, root *os.Root, job string, m wire.RegistryImageIdentity, record bool) error {
	if e := privateJob(root, job); e != nil {
		return e
	}
	if e := l.verifyStarted(ctx, root, job, m); e != nil {
		return e
	}
	// Ordinary continuation requires an already acknowledged or explicitly
	// reconciled receipt. Missing receipts never initiate automatic recovery.
	_, receiptErr := root.Lstat(job + "/loaded.json")
	if receiptErr != nil && (!os.IsNotExist(receiptErr) || !record) {
		return errors.New("REGISTRY_RECOVERY_REQUIRED")
	}
	image, e := l.docker.ReconcileRegistryImage(ctx)
	if e != nil {
		return e
	}
	if os.IsNotExist(receiptErr) {
		return privateWrite(root, job+"/loaded.json", image)
	}
	var original inference.Image
	if readPrivateJSON(root, job+"/loaded.json", l.config.Verification.MaximumMetadataBytes, &original) != nil || !reflect.DeepEqual(original, image) {
		return errors.New("REGISTRY_CONTENT_INVALID")
	}
	return nil
}

// ReconcileStoredTask is an explicit local inspect operation. It has no control
// credential, lease, result acknowledgement, image load or execution authority.
func (l *Loader) ReconcileStoredTask(ctx context.Context, task string) error {
	if !registryID.MatchString(task) {
		return errors.New("REGISTRY_MANIFEST_INVALID")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(l.config.OperationSeconds)*time.Second)
	defer cancel()
	root, e := os.OpenRoot(l.config.Root)
	if e != nil {
		return e
	}
	defer root.Close()
	job := "task-" + task
	m, e := l.taskIntent(root, job)
	if e != nil {
		return e
	}
	return l.reconcileImage(ctx, root, job, m.ImageIdentity(), true)
}

func (l *Loader) finalTaskAuthority(ctx context.Context, path, lease string, command wire.InferenceCommand, original wire.RegistryManifest) (wire.RegistryManifest, error) {
	current, e := l.control.RegistryManifest(ctx, path, lease, command)
	if e != nil {
		return wire.RegistryManifest{}, e
	}
	if !sameTaskImageIntent(original, current) {
		return wire.RegistryManifest{}, errors.New("REGISTRY_GRANT_STALE")
	}
	return current, nil
}
