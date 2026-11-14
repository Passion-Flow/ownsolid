package database_test

import (
 "os"
 "strings"
 "testing"
 "github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
 "github.com/jackc/pgx/v5/pgxpool"
)

func TestAcceptedSchema58ProductionMigrator(t *testing.T) {
 t.Run("integrated_fresh_test",func(t *testing.T){
  raw,err:=os.ReadFile(os.Getenv("PROJECT_ONE_TEST_DSN_FILE"));if err!=nil {t.Fatal("owned fixture input required")}
  c,err:=pgxpool.ParseConfig(strings.TrimSpace(string(raw)));if err!=nil {t.Fatal("invalid fixture input")};c.ConnConfig.Database="integrated_fresh_test"
  p,err:=pgxpool.NewWithConfig(t.Context(),c);if err!=nil {t.Fatal(err)};defer p.Close()
  if database.SchemaVersion!=58||database.TableCount!=118 {t.Fatal("accepted predecessor source required")}
  for range 2 {if err:=database.Migrate(t.Context(),p);err!=nil {t.Fatal(err)}}
  if err:=database.Check(t.Context(),p);err!=nil {t.Fatal(err)}
 })
}
