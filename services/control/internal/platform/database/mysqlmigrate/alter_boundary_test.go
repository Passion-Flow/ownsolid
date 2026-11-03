package mysqlmigrate

import "testing"

func TestMySQLEvolutionOwnedAlterTokenBoundary(t *testing.T) {
	for _, statement := range []string{"ALTER TABLE users ADD COLUMN value INT;", "ALTER TABLE users\n    MODIFY locale VARCHAR(35);", "ALTER TABLE users\tADD COLUMN value INT;", "ALTER TABLE users\r\n DROP CHECK test_check;"} {
		if !ownedAlter(statement, "users") {
			t.Fatal("valid owned token boundary refused")
		}
	}
	for _, statement := range []string{"ALTER TABLE users_other ADD COLUMN value INT;", "ALTER TABLE users2 ADD COLUMN value INT;", "ALTER TABLE users", "ALTER TABLE users` ADD COLUMN value INT;", "ALTER TABLE users\x00 ADD COLUMN value INT;", "ALTER TABLE other\nADD COLUMN value INT;"} {
		if ownedAlter(statement, "users") {
			t.Fatal("unowned token boundary admitted")
		}
	}
}
