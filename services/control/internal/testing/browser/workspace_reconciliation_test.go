package browser_test

import "testing"

func TestM20WorkspaceReconciliationBrowser(t *testing.T) {
	testProductionBrowser(t, "workspaces/reconciliation-flow.mjs", "workspace-reconciliation-browser", true)
}
