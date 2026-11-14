//go:build cgo && odbc_manager && repository_integration && dameng_repository_integration

package middleware

import (
 "context"
 "crypto/rand"
 "fmt"
 "os"
 "regexp"
 "testing"
 "time"
 "github.com/google/uuid"
 "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
 "github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
 "github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type damengStory struct {
 ctx context.Context
 store transaction.Store
 owner *identity.Service
 admin identity.Auth
 tenant, workspace, user string
}

// Requires an operator-provisioned fresh disposable full catalog. No DDL, driver
// download, credential logging, database deletion or engine fallback occurs here.
func newDamengStory(t *testing.T) damengStory {
 t.Helper()
 c, e := dameng.LoadConnectionConfiguration(os.Getenv("PROJECT_ONE_DAMENG_MIDDLEWARE_CONFIGURATION"), "")
 if e != nil || !regexp.MustCompile(`^PO_MIDDLEWARE_[A-F0-9]{16}_TEST$`).MatchString(c.Schema) { t.Fatal("EXACT_NATIVE_DISPOSABLE_CONFIGURATION_REQUIRED") }
 ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
 t.Cleanup(cancel)
 kernel, _, e := dameng.Open(ctx, c)
 if kernel != nil {
  t.Cleanup(func() {
   for attempt := 0; attempt < 2; attempt++ {
    closing, stop := context.WithTimeout(context.Background(), time.Duration(c.CleanupSeconds)*time.Second)
    state, e := kernel.Close(closing); stop()
    if e == nil && state.State == "closed" && state.PhysicalOwners == 0 && state.CleanupJobs == 0 { return }
   }
   t.Error("NATIVE_OWNER_DRAIN_INCOMPLETE: retain fixture and diagnostics")
  })
 }
 if e != nil { t.Fatal(e) }
 store, e := transaction.FromDameng(kernel)
 if e != nil || store.Dialect() != transaction.Dameng { t.Fatal("NATIVE_STORE_REQUIRED",e) }
 g := damengStory{ctx:ctx,store:store,tenant:uuid.NewString(),workspace:uuid.NewString(),user:uuid.NewString()}
 e = store.Within(ctx,false,func(ctx context.Context,q transaction.Executor) error {
  var ready int
  if e := q.QueryRow(ctx,transaction.SQL(transaction.Dameng,`SELECT count(*) FROM installation_state WHERE id=1 AND bootstrap_complete=0 AND tenant_id IS NULL AND default_workspace_id IS NULL AND primary_admin_user_id IS NULL`)).Scan(&ready); e != nil { return e }
  if ready != 1 { return fmt.Errorf("FRESH_NATIVE_FOUNDATION_REQUIRED") }
  for _, table := range []string{"tenants","workspaces","users","sessions","workspace_memberships","audit_events","middleware_instances","middleware_operations"} {
   var count int
   if e := q.QueryRow(ctx,transaction.SQL(transaction.Dameng,"SELECT count(*) FROM "+table)).Scan(&count); e != nil { return e }
   if count != 0 { return fmt.Errorf("EMPTY_NATIVE_SCHEMA_REQUIRED") }
  }
  for _, s := range []transaction.Statement{
   transaction.SQL(transaction.Dameng,`INSERT INTO tenants(id,name,status) VALUES(?,'Synthetic native tenant','active')`,g.tenant),
   transaction.SQL(transaction.Dameng,`INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Synthetic native workspace','active')`,g.workspace,g.tenant),
   transaction.SQL(transaction.Dameng,`INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES(?,'native-synthetic@example.invalid','native-synthetic@example.invalid','Synthetic actor','en','active',1)`,g.user),
   transaction.SQL(transaction.Dameng,`INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role,authority_id) VALUES(?,?,?,'owner',?)`,g.user,g.tenant,g.workspace,uuid.NewString()),
   transaction.SQL(transaction.Dameng,`UPDATE installation_state SET bootstrap_complete=1,tenant_id=?,default_workspace_id=?,primary_admin_user_id=?,bootstrapped_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE id=1 AND bootstrap_complete=0`,g.tenant,g.workspace,g.user),
  } { n,e:=q.Exec(ctx,s); if e!=nil {return e}; if n!=1{return fmt.Errorf("NATIVE_SEED_ROWCOUNT_INVALID")} }
  return nil
 })
 if e != nil {t.Fatal(e)}
 g.owner=&identity.Service{Storage:store,HMACKey:make([]byte,32)}
 if _,e=rand.Read(g.owner.HMACKey);e!=nil{t.Fatal(e)}
 g.owner.Config.Server.AdminOrigin="https://admin.example.invalid"
 g.owner.Config.Server.PortalOrigin="https://portal.example.invalid"
 var issued identity.IssuedSession
 e=store.Within(ctx,false,func(ctx context.Context,q transaction.Executor) error {
  var e error; issued,e=g.owner.IssueSessionWithin(ctx,q,g.user,1,"admin","",uuid.NewString(),nil);return e
 })
 if e!=nil{t.Fatal(e)}
 g.admin,e=g.owner.Authenticate(ctx,issued.Token,"admin")
 if e!=nil{t.Fatal(e)}
 t.Logf("Retain exact native synthetic fixture for evidence/owned cleanup: schema=%s tenant=%s",c.Schema,g.tenant)
 return g
}
func (g damengStory) exec(t *testing.T, sql string, args ...any) {
 t.Helper(); if _,e:=g.store.Exec(g.ctx,transaction.SQL(transaction.Dameng,sql,args...));e!=nil{t.Fatal(e)}
}
func (g damengStory) count(t *testing.T, sql string, args ...any) int {
 t.Helper(); var count int
 if e:=g.store.QueryRow(g.ctx,transaction.SQL(transaction.Dameng,sql,args...)).Scan(&count);e!=nil{t.Fatal(e)}
 return count
}

// Only acknowledgement is scripted. SQL, commit and readback use the actual DM
// driver; this is not evidence of physical wire loss or driver fault injection.
type damengDropCommit struct { transaction.Store; dropped bool; after int; cancel context.CancelFunc }
func (s *damengDropCommit) Within(ctx context.Context, readOnly bool, fn func(context.Context,transaction.Executor) error) error {
 if s.dropped{s.after++}
 e:=s.Store.Within(ctx,readOnly,fn)
 if e==nil && !s.dropped {s.dropped=true;if s.cancel!=nil{s.cancel()};return &transaction.Error{Kind:transaction.CommitUnknown}}
 return e
}
