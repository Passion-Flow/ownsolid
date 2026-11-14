import json
import pathlib

scratch = pathlib.Path(__file__).resolve().parent
path = pathlib.Path("services/control/internal/modules/deployments/observation_scope_test.go").resolve()
text = path.read_text()

def replace(source, result):
    global text
    assert text.count(source) == 1, source
    text = text.replace(source, result)

replace('import (\n', 'import (\n "context"\n "fmt"\n')
replace('func TestMutualTLSInferenceObservationCost(t *testing.T) {', '''func TestMutualTLSInferenceObservationCost(t *testing.T) {
 const smallFleet, largeFleet = 50, 3000
 measurements := map[int]map[string]agentcost.Cost{}
 for _, fleet := range []int{smallFleet, largeFleet} {
  t.Run(fmt.Sprint(fleet), func(t *testing.T) {
   measurements[fleet] = measureMutualTLSInferenceObservationCost(t, fleet)
  })
 }
 if t.Failed() { return }
 for _, name := range []string{"cold first report", "warm update", "foreign credential", "retained current", "stale revision"} {
  small, large := measurements[smallFleet][name], measurements[largeFleet][name]
  t.Logf("%s: statements %d/%d rows %d/%d blocks %d/%d", name, len(small.Statements), len(large.Statements), small.Rows(), large.Rows(), small.Blocks(), large.Blocks())
  if len(small.Statements) == 0 || len(large.Statements) == 0 {
   t.Errorf("%s did not record both measurements", name)
  }
  for _, violation := range agentcost.Violations(wire.CallHeartbeat.Cost(), small, large) {
   t.Errorf("%s: %s", name, violation)
  }
 }
}

func measureMutualTLSInferenceObservationCost(t *testing.T, fleet int) map[string]agentcost.Cost {''')
replace('ctx := t.Context()\n\tvar recorder', 'ctx := t.Context()\n measured := map[string]agentcost.Cost{}\n\tvar recorder')
replace('h.Inference = &wire.InferenceObservation{GPUs: []wire.NVIDIAGPU{}, ObservedAt: now}', '''h.Inference = &wire.InferenceObservation{GPUs: []wire.NVIDIAGPU{}, ObservedAt: now}
 seedInferenceCostFleet(t, account.DB, node.NodeID, admin.Me.User.ID, fleet)
 observe := func(t *testing.T, name string, heartbeat func()) {
  t.Helper()
  holder, err := account.DB.Begin(ctx)
  if err != nil { t.Fatal(err) }
  defer holder.Rollback(context.WithoutCancel(ctx))
  for _, row := range []struct{ sql string; args []any }{
   {"SELECT 1 FROM installation_state WHERE id=1 FOR SHARE", nil},
   {"SELECT 1 FROM tenants WHERE id=$1 FOR SHARE", []any{admin.Me.Workspace.TenantID}},
   {"SELECT 1 FROM workspaces WHERE id=$1 FOR SHARE", []any{admin.Me.Workspace.ID}},
   {"SELECT 1 FROM users WHERE id=$1 FOR SHARE", []any{admin.Me.User.ID}},
   {"SELECT 1 FROM workspace_memberships WHERE workspace_id=$1 AND user_id=$2 FOR SHARE", []any{admin.Me.Workspace.ID, admin.Me.User.ID}},
  } {
   if _, err := holder.Exec(ctx, row.sql, row.args...); err != nil { t.Fatal(err) }
  }
  measured[name] = checkHeartbeatCost(t, &recorder, heartbeat)
 }''')
replace('checkHeartbeatCost(t, &recorder, func() {\n\t\t\t\tif probe', 'observe(t, probe.name, func() {\n\t\t\t\tif probe')
replace('checkHeartbeatCost(t, &recorder, func() {\n\t\t\tcold.Call', 'observe(t, "retained current", func() {\n\t\t\tcold.Call')
replace('checkHeartbeatCost(t, &recorder, func() {\n\t\t\t\tcold.Call', 'observe(t, "stale revision", func() {\n\t\t\t\tcold.Call')
replace('})\n}\n\nfunc checkHeartbeatCost', '''})
 return measured
}

func seedInferenceCostFleet(t *testing.T, db *pgxpool.Pool, node, actor string, fleet int) {
 t.Helper()
 ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
 defer cancel()
 err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
  for _, seed := range []struct{ sql string; args []any }{
   {"SET LOCAL statement_timeout='90s'", nil},
   {"SET LOCAL auto_explain.log_min_duration=-1", nil},
   {`INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,protocol_version,facts,execution_profile,capabilities,credential_hash,last_heartbeat_at)
     SELECT gen_random_uuid(),m.tenant_id,m.workspace_id,'inference-cost-peer-'||g,m.status,m.protocol_version,m.facts,m.execution_profile,m.capabilities,sha256(g::text::bytea),clock_timestamp()
     FROM managed_nodes m,generate_series(1,$1::integer) g WHERE m.id=$2`, []any{fleet,node}},
   {`INSERT INTO inference_nodes(tenant_id,workspace_id,node_id,observation,site_id,cell_id)
     SELECT tenant_id,workspace_id,id,'{"gpus":[]}',site_id,cell_id FROM managed_nodes WHERE name LIKE 'inference-cost-peer-%'`, nil},
   {`INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at,payload,completed_at)
     SELECT gen_random_uuid(),n.tenant_id,n.workspace_id,n.id,'collect_facts.v1','succeeded',gen_random_uuid(),$1,clock_timestamp(),'{}',clock_timestamp()
     FROM managed_nodes n,generate_series(1,4) WHERE n.name LIKE 'inference-cost-peer-%'`, []any{actor}},
   {`INSERT INTO audit_events(id,occurred_at,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,request_id)
     SELECT gen_random_uuid(),clock_timestamp(),n.tenant_id,n.workspace_id,'system','node.task_claimed','success','node_task',gen_random_uuid(),gen_random_uuid()
     FROM managed_nodes n,generate_series(1,4) WHERE n.name LIKE 'inference-cost-peer-%'`, nil},
   {"ANALYZE managed_nodes, inference_nodes, node_tasks, audit_events", nil},
  } {
   if _, err := tx.Exec(ctx, seed.sql, seed.args...); err != nil { return err }
  }
  return nil
 })
 if err != nil { t.Fatal(err) }
}

func checkHeartbeatCost''')
replace('heartbeat func()) {\n\tt.Helper()\n\tcost, err := recorder.Record(heartbeat)', 'heartbeat func()) agentcost.Cost {\n\tt.Helper()\n\tcost, err := recorder.Record(heartbeat)')
replace('\n}\n\nfunc TestInferenceObservationKeepsNodeScopeOnConcurrentFirstWrites', '\n return cost\n}\n\nfunc TestInferenceObservationKeepsNodeScopeOnConcurrentFirstWrites')
new_path = scratch / "observation_scope_test.go"
new_path.write_text(text)
(scratch / "inference-cost-overlay.json").write_text(json.dumps({"Replace": {str(path): str(new_path)}}))
