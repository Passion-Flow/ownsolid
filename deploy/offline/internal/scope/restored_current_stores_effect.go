package scope

import (
	"context"
	"os"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

func (l *locked) executeCurrentStoresStep(ctx context.Context, c currentStoresEngine, p RestoredCurrentStoresPlan, step restoreDatabaseStep, files []engine.ConfigurationFile, reconcile bool) (string, error) {
	bad := func() (string, error) { return "", failure("OFFLINE_SCOPE_CURRENT_STORES_EFFECT_UNRESOLVED") }
	t := p.Tasks[0]
	owner := t.VolumeOwner
	switch step.phase {
	case "plan-publish":
		if _, e := l.readCurrentStoresPlan(currentStoresPlanRecord(p)); e == nil {
			return "", nil
		}
		if _, e := l.root.Lstat(currentStoresPlanFile(p.GenerationID)); !os.IsNotExist(e) {
			return bad()
		}
		if e := writeNew(l.root, currentStoresPlanFile(p.GenerationID), encode(p)); e != nil {
			return "", e
		}
		_, e := l.readCurrentStoresPlan(currentStoresPlanRecord(p))
		return "", e
	case "volume-create":
		if !reconcile {
			if _, e := c.CreateVolume(ctx, owner); e != nil {
				return "", e
			}
		}
		if _, found, e := c.InspectVolume(ctx, owner); e != nil || !found {
			return bad()
		}
	case "utility-create":
		if !reconcile {
			if _, e := c.CreateUtility(ctx, owner, p.Initializer, t.UID, t.GID); e != nil {
				return "", e
			}
		}
		u, found, e := c.InspectUtility(ctx, owner, p.Initializer, t.UID, t.GID)
		if e != nil || !found {
			return bad()
		}
		return u.ID, nil
	case "payload-write":
		if !reconcile {
			if e := c.PutConfiguration(ctx, owner, p.Initializer, t.UID, t.GID, files); e != nil {
				return "", e
			}
		}
		o, e := c.InspectConfiguration(ctx, owner, p.Initializer, t.UID, t.GID, files)
		if e != nil || o.Status != engine.ConfigurationPayloadVerified {
			return bad()
		}
	case "utility-remove":
		if !reconcile {
			if e := c.RemoveUtility(ctx, owner, p.Initializer, t.UID, t.GID); e != nil {
				return "", e
			}
		}
		if _, found, e := c.InspectUtility(ctx, owner, p.Initializer, t.UID, t.GID); e != nil || found {
			return bad()
		}
	case "task-create", "task-execute", "task-remove":
		for _, task := range p.Tasks {
			if task.Kind == step.kind {
				id := ""
				for i, r := range currentStoresState(l.events).completed {
					if currentStoresSteps(r.StoreCount)[i] == (restoreDatabaseStep{"task-create", task.Kind}) {
						id = r.ObjectID
					}
				}
				return l.executeRestoreTask(ctx, c, task, step.phase, id, files, reconcile)
			}
		}
		return bad()
	case "volume-remove":
		if !reconcile {
			if e := c.RemoveStoreRestoreVolume(ctx, owner); e != nil {
				return "", e
			}
		}
		if _, found, e := c.InspectVolume(ctx, owner); e != nil || found {
			return bad()
		}
	case "complete":
		if _, found, e := c.InspectVolume(ctx, owner); e != nil || found {
			return bad()
		}
	default:
		return bad()
	}
	return "", nil
}

func (l *locked) abortCurrentStores(ctx context.Context, c currentStoresEngine, p RestoredCurrentStoresPlan) (RestoredCurrentStoresResult, error) {
	result := func() RestoredCurrentStoresResult { return l.currentStoresResult() }
	if _, e := l.checkEngine(ctx, c); e != nil {
		return result(), e
	}
	s := currentStoresState(l.events)
	if s.aborted {
		return result(), nil
	}
	r := *s.record
	r.ObjectID = ""
	id, reference := s.pending, s.abortReference
	if !s.aborting {
		var e error
		id, e = nonce()
		if e != nil {
			return result(), e
		}
		reference = s.pending
		if e = l.appendEvent(Event{OperationID: id, ReferenceOperationID: reference, Action: "current_stores_abort", Outcome: "started", RestoredCurrentStores: &r}); e != nil {
			return result(), e
		}
	}
	e := l.removeCurrentStoresActors(ctx, c, p)
	outcome, code := "completed", ""
	if e != nil {
		outcome, code = "uncertain", "OFFLINE_SCOPE_CURRENT_STORES_CLEANUP_UNRESOLVED"
	}
	if je := l.appendEvent(Event{OperationID: id, ReferenceOperationID: reference, Action: "current_stores_abort", Outcome: outcome, Code: code, RestoredCurrentStores: &r}); je != nil {
		return result(), je
	}
	return result(), e
}
func (l *locked) removeCurrentStoresActors(ctx context.Context, c currentStoresEngine, p RestoredCurrentStoresPlan) error {
	if len(p.Tasks) == 0 {
		return nil
	}
	t := p.Tasks[0]
	owner := t.VolumeOwner
	if _, found, e := c.InspectVolume(ctx, owner); e != nil {
		return e
	} else if !found {
		objects, e := c.RuntimeCandidates(ctx, p.ScopeID, false)
		if e != nil {
			return e
		}
		for _, object := range objects {
			if object.Labels["io.projectone.operation"] == p.GenerationID {
				return failure("OFFLINE_SCOPE_CURRENT_STORES_CLEANUP_ORPHAN_PRESERVED")
			}
		}
		return nil
	}
	for _, task := range p.Tasks {
		o, found, e := c.InspectDatabaseTask(ctx, task)
		if e != nil {
			return e
		}
		if found {
			if o.State.Running {
				if e = c.StopDatabaseTask(ctx, task, o.ID, l.config.Compose.Process.StopSeconds); e != nil {
					return e
				}
			}
			if e = c.RemoveDatabaseTask(ctx, task, o.ID); e != nil {
				return e
			}
			if _, found, e = c.InspectDatabaseTask(ctx, task); e != nil || found {
				return failure("OFFLINE_SCOPE_CURRENT_STORES_CLEANUP_TASK_UNRESOLVED")
			}
		}
	}
	if _, found, e := c.InspectUtility(ctx, owner, p.Initializer, t.UID, t.GID); e != nil {
		return e
	} else if found {
		if e = c.RemoveUtility(ctx, owner, p.Initializer, t.UID, t.GID); e != nil {
			return e
		}
	}
	if e := c.RemoveStoreRestoreVolume(ctx, owner); e != nil {
		return e
	}
	if _, found, e := c.InspectVolume(ctx, owner); e != nil || found {
		return failure("OFFLINE_SCOPE_CURRENT_STORES_CLEANUP_VOLUME_UNRESOLVED")
	}
	return nil
}
