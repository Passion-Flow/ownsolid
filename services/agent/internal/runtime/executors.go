package runtime

import (
	"github.com/Passion-Flow/ownsolid/services/agent/internal/inference"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/kubernetes"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/training"
)

// executors are the managers this node runs tasks with. Each is nil unless the node's
// execution profile runs that kind of task.
type executors struct {
	components   componentLifecycle
	inference    *inference.Manager
	training     *training.Manager
	preparations *modelpreparations.Manager
	registry     taskImageLoader
	kubernetes   *kubernetes.Manager
}
