import json
import pathlib

scratch = pathlib.Path(__file__).resolve().parent
source = (scratch / "observation_scope_test.go").read_text()
path = pathlib.Path("services/control/internal/modules/deployments/observation_scope_test.go").resolve()
header = source.split('func measureMutualTLSInferenceObservationCost(t *testing.T, fleet int) map[string]agentcost.Cost {', 1)[1].split('\n\th.Inference =', 1)[0]
header = header.replace('wire.DockerInferenceProfile','wire.SystemdComponentsProfile').replace('Inference cost node', 'Component cost node').replace('inference-cost-node', 'component-cost-node').replace(' + "/heartbeat"', '')
lock = source.split('\n\tobserve := func(', 1)[1].split('\n\tfor _, probe :=', 1)[0]
lock = lock.replace('measured[name] = checkHeartbeatCost(t, &recorder, heartbeat)', 'cost, err := recorder.Record(heartbeat)\n if err != nil { t.Fatal(err) }; measured[name] = cost')
seed = source.split('func seedInferenceCostFleet', 1)[1].split('\nfunc checkHeartbeatCost', 1)[0]
seed = seed.replace('inference-cost-peer-', 'component-cost-peer-').replace(
    'INSERT INTO inference_nodes(tenant_id,workspace_id,node_id,observation,site_id,cell_id)\n     SELECT tenant_id,workspace_id,id,\'{"gpus":[]}\',site_id,cell_id',
    "INSERT INTO managed_components(tenant_id,workspace_id,node_id,component,version,desired_state,observed_state,managed,observation,observed_at,site_id,cell_id)\n     SELECT tenant_id,workspace_id,id,'node_exporter','1.12.1','not_installed','not_installed',false,'{}',clock_timestamp(),site_id,cell_id",
).replace('ANALYZE managed_nodes, inference_nodes','ANALYZE managed_nodes, managed_components')
append = '''
func TestComponentAgentExchangeCost(t *testing.T) {
 const smallFleet, largeFleet = 50, 3000
 samples := map[int]map[string]agentcost.Cost{}
 for _, fleet := range []int{smallFleet, largeFleet} {
  t.Run(fmt.Sprint(fleet), func(t *testing.T) { samples[fleet] = measureComponentExchangeCost(t, fleet) })
 }
 if t.Failed() { return }
 for _, exchange := range []struct { name string; call wire.Call }{
  {"first component report",wire.CallHeartbeat}, {"component update",wire.CallHeartbeat}, {"foreign credential",wire.CallHeartbeat},
  {"component claim",wire.CallClaim}, {"component renew",wire.CallRenew}, {"component result",wire.CallResult},
  {"component result replay",wire.CallResult},
 } {
  a, b := samples[smallFleet][exchange.name], samples[largeFleet][exchange.name]
  t.Logf("%s: statements %d/%d rows %d/%d blocks %d/%d", exchange.name, len(a.Statements), len(b.Statements), a.Rows(), b.Rows(), a.Blocks(), b.Blocks())
  if len(a.Statements)==0 || len(b.Statements)==0 { t.Errorf("%s: missing measurements", exchange.name) }
  for _, violation := range agentcost.Violations(exchange.call.Cost(),a,b) { t.Errorf("%s: %s", exchange.name,violation) }
 }
}

func measureComponentExchangeCost(t *testing.T, fleet int) map[string]agentcost.Cost {''' + header + '''
 seedComponentCostFleet(t, account.DB, node.NodeID, admin.Me.User.ID, fleet)
 observe := func(''' + lock + '''
 h.Component = &wire.ComponentObservation{Component: wire.NodeExporter, Version: wire.NodeExporterVersion, State: "not_installed", ObservedAt: now}
 client.Headers = map[string]string{"Authorization": "Bearer " + node.Credential}
 observe(t, "first component report", func() { client.Call("POST",path+"/heartbeat",h,200,"DispositionResponse") })
 h.Component.State, h.Component.ErrorCode = "unknown", "COMPONENT_SYSTEMD_UNAVAILABLE"
 observe(t, "component update", func() { client.Call("POST",path+"/heartbeat",h,200,"DispositionResponse") })
 client.Headers["Authorization"] = "Bearer " + security.Token()
 observe(t, "foreign credential", func() {
  if code := client.Problem("POST", path+"/heartbeat", h, 401); code != "AGENT_UNAUTHORIZED" { t.Fatalf("foreign credential: %s", code) }
 })
 client.Headers["Authorization"] = "Bearer " + node.Credential
 task, err := handler.Components.Service.Request(ctx, admin, node.NodeID, "start", uuid.NewString(), "Component cost probe", uuid.NewString())
 if err != nil { t.Fatal(err) }
 var leased struct { Lease *wire.Lease }
 observe(t, "component claim", func() {
  body := client.Call("POST",path+"/tasks/claims",wire.ClaimRequest{},200,"")
  data, err := json.Marshal(body)
  if err != nil || json.Unmarshal(data,&leased)!=nil || leased.Lease==nil || leased.Lease.Component==nil || leased.Lease.TaskID!=task.ID || leased.Lease.Component.Action!="start" { t.Fatal("expected exact component claim",err) }
 })
 taskPath := path+"/tasks/"+task.ID
 observe(t,"component renew",func() { client.Call("POST",taskPath+"/renewals",map[string]string{"lease_token":leased.Lease.LeaseToken},200,"") })
 failed := wire.Result{LeaseToken:leased.Lease.LeaseToken,Outcome:"failure",ErrorCode:h.Component.ErrorCode,Component:h.Component}
 observe(t,"component result",func() { client.Call("POST",taskPath+"/results",failed,200,"") })
 observe(t,"component result replay",func() { client.Call("POST",taskPath+"/results",failed,200,"") })
 view, err := handler.Components.Service.Get(ctx,admin,admin.Me.Workspace.ID,node.NodeID)
 if err!=nil || view.Observation==nil || view.Observation.ErrorCode!=failed.ErrorCode { t.Fatal("component result did not persist",err) }
 var state string
 if err := account.DB.QueryRow(ctx,"SELECT state FROM node_tasks WHERE id=$1",task.ID).Scan(&state); err!=nil || state!="failed" { t.Fatal("component outcome did not settle",state,err) }
 return measured
}

func seedComponentCostFleet''' + seed
target = scratch / "component_scope_test.go"
target.write_text(source + append)
(scratch / "component-cost-overlay.json").write_text(json.dumps({"Replace": {str(path): str(target)}}))
