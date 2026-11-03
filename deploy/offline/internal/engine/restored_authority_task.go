package engine

const DatabaseRestoredAuthInvalidate = "invalidate-restored-auth"

func (t DatabaseTask) restoredAuthority() bool { return t.Kind == DatabaseRestoredAuthInvalidate }

func (t DatabaseTask) restoredAuthorityArguments() []string {
	if !t.restoredAuthority() {
		return nil
	}
	return []string{"--reason", t.Reason}
}
