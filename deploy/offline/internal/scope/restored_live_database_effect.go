package scope

import (
	"context"
	"os"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

func (l *locked) executeLiveDatabaseStep(ctx context.Context, c liveDatabaseEngine, p RestoredLiveDatabasePlan, step restoreDatabaseStep, files []engine.ConfigurationFile, reconcile bool) (string, error) {
	bad := func() (string, error) { return "", failure("OFFLINE_SCOPE_LIVE_DATABASE_EFFECT_UNRESOLVED") }
	t := p.Tasks[0]
	owner := t.VolumeOwner
	switch step.phase {
	case "plan-publish":
		if _, e := l.readLiveDatabasePlan(liveDatabasePlanRecord(p)); e == nil {
			return "", nil
		}
		if _, e := l.root.Lstat(liveDatabasePlanFile(p.GenerationID)); !os.IsNotExist(e) {
			return bad()
		}
		if e := writeNew(l.root, liveDatabasePlanFile(p.GenerationID), encode(p)); e != nil {
			return "", e
		}
		_, e := l.readLiveDatabasePlan(liveDatabasePlanRecord(p))
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
				for i, r := range liveDatabaseState(l.events).completed {
					if liveDatabaseSteps(p.NativeRuntime)[i] == (restoreDatabaseStep{"task-create", task.Kind}) {
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

func (l *locked) abortLiveDatabase(ctx context.Context, c liveDatabaseEngine, p RestoredLiveDatabasePlan) (RestoredLiveDatabaseResult, error) {
	result := func() RestoredLiveDatabaseResult { return l.liveDatabaseResult() }
	if _, e := l.checkEngine(ctx, c); e != nil {
		return result(), e
	}
	s := liveDatabaseState(l.events)
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
		if e = l.appendEvent(Event{OperationID: id, ReferenceOperationID: reference, Action: "live_database_abort", Outcome: "started", RestoredLiveDatabase: &r}); e != nil {
			return result(), e
		}
	}
	e := l.removeLiveDatabaseActors(ctx, c, p)
	outcome, code := "completed", ""
	if e != nil {
		outcome, code = "uncertain", "OFFLINE_SCOPE_LIVE_DATABASE_CLEANUP_UNRESOLVED"
	}
	if je := l.appendEvent(Event{OperationID: id, ReferenceOperationID: reference, Action: "live_database_abort", Outcome: outcome, Code: code, RestoredLiveDatabase: &r}); je != nil {
		return result(), je
	}
	return result(), e
}
func (l *locked) removeLiveDatabaseActors(ctx context.Context, c liveDatabaseEngine, p RestoredLiveDatabasePlan) error {
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
				return failure("OFFLINE_SCOPE_LIVE_DATABASE_CLEANUP_ORPHAN_PRESERVED")
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
				return failure("OFFLINE_SCOPE_LIVE_DATABASE_CLEANUP_TASK_UNRESOLVED")
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
		return failure("OFFLINE_SCOPE_LIVE_DATABASE_CLEANUP_VOLUME_UNRESOLVED")
	}
	return nil
}
