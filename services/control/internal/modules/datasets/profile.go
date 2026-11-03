package datasets

// Later connector sources are catalogued only by their enabled platform owner.
func (s *Service) postgresSourceQuery(upload, connectors string) string {
	if s.Identity.Config.DatasetRuntime() {
		return upload
	}
	return connectors
}
