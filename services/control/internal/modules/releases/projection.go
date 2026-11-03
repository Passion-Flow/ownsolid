package releases

// VerifyProjection uses the production reader and actual process EUID. The
// startup verifier has no database/session/worker constructor and receives no
// permission to publish, repair or write the projection.
func VerifyProjection(c Config) error {
	_, err := readSnapshot(c)
	return err
}
