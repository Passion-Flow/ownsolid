package database_test

import (
 "os"
 "strings"
 "testing"
 "github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
 "github.com/jackc/pgx/v5/pgxpool"
)

func TestFormalSchema60ProductionMigratorAfterRestore(t *testing.T) {
 raw,err:=os.ReadFile(os.Getenv("PROJECT_ONE_TEST_DSN_FILE"));if err!=nil {t.Fatal("owned fixture input required")}
 for _,suffix:=range []string{"fresh","native59","55","56","57","58","59","60"} {
  t.Run(suffix,func(t *testing.T){
   config,err:=pgxpool.ParseConfig(strings.TrimSpace(string(raw)));if err!=nil {t.Fatal("invalid owned fixture input")}
   config.ConnConfig.Database="integrated_"+suffix+"_test"
   db,err:=pgxpool.NewWithConfig(t.Context(),config);if err!=nil {t.Fatal(err)};defer db.Close()
   for range 2 {if err:=database.Migrate(t.Context(),db);err!=nil {t.Fatal("production migration",err)}}
   if err:=database.Check(t.Context(),db);err!=nil {t.Fatal("production admission",err)}
   var tables,entries int
   if err:=db.QueryRow(t.Context(),"SELECT count(*) FROM pg_class WHERE relnamespace=current_schema()::regnamespace AND relkind IN ('r','p') AND NOT relispartition").Scan(&tables);err!=nil||tables!=120 {t.Fatal("table count",tables,err)}
   if err:=db.QueryRow(t.Context(),"SELECT count(*) FROM schema_migrations").Scan(&entries);err!=nil||entries!=60 {t.Fatal("ledger count",entries,err)}
  })
 }
}
