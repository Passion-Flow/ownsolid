package distribution_test

import (
 "errors"
 "testing"
 fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
 "github.com/jackc/pgx/v5"
 "github.com/jackc/pgx/v5/pgconn"
)

func staleSnapshotRollback(t *testing.T, script string, proposed bool) {
 t.Helper()
 c:=setupAccount(t,fixture.NewAtVersion(t,58));ctx:=t.Context();alias:=c.published(t)
 if _,err:=c.s.DB.Exec(ctx,aliasDescriptionCandidateUp);err!=nil {t.Fatal(err)}
 tx,err:=c.s.DB.BeginTx(ctx,pgx.TxOptions{IsoLevel:pgx.RepeatableRead});if err!=nil {t.Fatal(err)}
 defer tx.Rollback(ctx)
 var before string
 if err:=tx.QueryRow(ctx,"SELECT description FROM model_aliases WHERE id=$1",alias.ID).Scan(&before);err!=nil||before!="" {t.Fatal("pristine snapshot",before,err)}
 if _,err:=c.s.DB.Exec(ctx,"UPDATE model_aliases SET description='Retain writer committed after snapshot' WHERE id=$1",alias.ID);err!=nil {t.Fatal(err)}
 _,err=tx.Exec(ctx,script)
 if err==nil {t.Fatal("stale snapshot allowed the down script to drop newly committed description state")}
 var pg *pgconn.PgError
 if proposed && (!errors.As(err,&pg)||pg.Code!="25001") {t.Fatal("unexpected proposed isolation refusal",err)}
 if err:=tx.Rollback(ctx);err!=nil {t.Fatal(err)}
 var retained string
 if err:=c.s.DB.QueryRow(ctx,"SELECT description FROM model_aliases WHERE id=$1",alias.ID).Scan(&retained);err!=nil||retained!="Retain writer committed after snapshot" {t.Fatal("committed writer not retained",retained,err)}
 t.Log("downgrade refused stale snapshot",err)
}

func TestFormalAliasDescriptionRollbackRejectsStaleSnapshot(t *testing.T) {
 staleSnapshotRollback(t,aliasDescriptionCandidateDown,false)
}

func TestFormalAliasDescriptionRollbackIsolationPrototype(t *testing.T) {
 staleSnapshotRollback(t,"SET TRANSACTION ISOLATION LEVEL READ COMMITTED;\n"+aliasDescriptionCandidateDown,true)
}
