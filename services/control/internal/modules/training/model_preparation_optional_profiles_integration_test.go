//go:build training_artifact_integration && model_lifecycle_admission_integration

package training_test

import "testing"

// Test-only registration: an explicitly requested profile fails if its owning build tag is absent.
// This is not a runtime adapter or a substituted product implementation.
var preparationPairedRestoreProfile func(*testing.T, preparationScenario)
