package compose_test

import (
	"github.com/google/uuid"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the production immutable materializer in a fresh labelled VM volume.
// The host source, Linux private child and runtime UID remain separate inputs.
func configureComposeLocalization(t *testing.T, root, configRoot, id, project string, env, base []string) ([]string, []string, string) {
	t.Helper()
	source := os.Getenv("PROJECT_ONE_STATIC_LOCALE_PACKAGE_DIRECTORY")
	resolved, e := filepath.EvalSymlinks(source)
	if e != nil || resolved != source || !filepath.IsAbs(source) || !strings.HasPrefix(source, os.Getenv("PROJECT_ONE_STATE_ROOT")+"/") {
		t.Fatal("EXACT_CURRENT_STATIC_LOCALE_COMPONENT_REQUIRED")
	}
	if st, e := os.Stat(source); e != nil || !st.IsDir() || st.Mode().Perm() != 0700 {
		t.Fatal("PRIVATE_LOCALE_COMPONENT_REQUIRED")
	}
	volume := project + "-localization"
	if exec.Command("docker", "volume", "inspect", volume).Run() == nil {
		t.Fatal("REFUSE_EXISTING_LOCALIZATION_FIXTURE_VOLUME")
	}
	if e := exec.Command("docker", "volume", "create", "--label", "projectone.owner=mvp-development", "--label", "projectone.scope="+id, volume).Run(); e != nil {
		t.Fatal("LOCALIZATION_FIXTURE_VOLUME_CREATE_FAILED")
	}
	env = append(env, "PROJECT_ONE_LOCALIZATION_PACKAGE_HOST_DIRECTORY="+source, "PROJECT_ONE_LOCALIZATION_POLICY_FILE="+filepath.Join(configRoot, "locale-policy.json"), "PROJECT_ONE_LOCALIZATION_VOLUME="+volume, "PROJECT_ONE_LOCALIZATION_SCOPE_ID="+uuid.NewString(), "PROJECT_ONE_LOCALIZATION_MOUNT_ROOT=/var/lib/projectone/locale-projection", "PROJECT_ONE_LOCALIZATION_INIT_SOURCE_UID="+os.Getenv("PROJECT_ONE_COMPOSE_LOCALIZATION_SOURCE_UID"), "PROJECT_ONE_LOCALIZATION_INIT_TIMEOUT_SECONDS=60", "PROJECT_ONE_LOCALIZATION_INIT_MEMORY_BYTES=134217728", "PROJECT_ONE_LOCALIZATION_INIT_CPUS=0.25", "PROJECT_ONE_LOCALIZATION_INIT_PIDS=16")
	base = append(base, "-f", filepath.Join(root, "deploy/compose/localization/compose.yaml"))
	return env, base, volume
}
