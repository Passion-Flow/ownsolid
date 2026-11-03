package migrate

func requiredGrants(c catalog) map[string]map[string]bool {
	out := map[string]map[string]bool{journalName: {"SELECT": true}}
	for _, t := range c.tables {
		out[t.Name] = map[string]bool{"SELECT": true, "INSERT": true, "UPDATE": true, "DELETE": true}
	}
	// DM checks referenced-table authority for cross-schema runtime FK writes.
	// Add REFERENCES only to actual referenced tables, never journal or ANY DDL.
	for _, t := range c.tables {
		for _, key := range t.Constraints {
			if key.Kind == "R" {
				out[key.Reference]["REFERENCES"] = true
			}
		}
	}
	return out
}

// DM's PUBLIC role includes own-object DML/grant operations and dictionary
// inspection. Those do not create objects or authorize another schema's DML.
// The runtime account must own no objects and receive only the exact grants
// above. CREATE/ALTER/DROP, cross-schema ANY DML and administration stay refused.
func allowedRuntimeSystemPrivilege(privilege string) bool {
	switch privilege {
	case "CREATE SESSION", "SELECT ANY DICTIONARY",
		"INSERT TABLE", "UPDATE TABLE", "DELETE TABLE", "SELECT TABLE", "DUMP TABLE", "REFERENCES TABLE", "REFERENCE TABLE", "GRANT TABLE",
		"INSERT VIEW", "UPDATE VIEW", "DELETE VIEW", "SELECT VIEW", "GRANT VIEW",
		"EXECUTE PROCEDURE", "GRANT PROCEDURE", "SELECT SEQUENCE", "GRANT SEQUENCE", "EXECUTE PACKAGE", "GRANT PACKAGE",
		"SELECT MATERIALIZED VIEW", "GRANT DOMAIN", "USAGE DOMAIN":
		return true
	}
	return false
}
func allowedRuntimeRole(role string) bool { return role == "PUBLIC" || role == "SVI" }
