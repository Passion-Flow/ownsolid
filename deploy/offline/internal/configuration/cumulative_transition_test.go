package configuration

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCumulativeUpgradeInputsAreExplicitBoundedAndIndependent(t *testing.T) {
	r := CumulativeUpgradeDefaults()
	r.PreparedRoot = "/verified/prepared-release"
	r.ArchiveSHA256 = strings.Repeat("a", 64)
	r.ManifestSHA256 = strings.Repeat("b", 64)
	r.BackupSource.BackupRoot = "/verified/backup"
	r.BackupSource.ManifestSHA256 = strings.Repeat("c", 64)
	r.ReadinessDialAddress = "127.0.0.1:8080"
	raw, _ := json.Marshal(r)
	got, e := LoadCumulativeUpgradeRequest(raw, []byte("PROJECT_ONE_OFFLINE_CUMULATIVE_UPGRADE_PAGE_SIZE=7\nPROJECT_ONE_OFFLINE_CUMULATIVE_UPGRADE_READINESS_DIAL_ADDRESS=[::1]:9090\nPROJECT_ONE_OFFLINE_CUMULATIVE_UPGRADE_READINESS_CA_NAME=issuer.pem\n"))
	if e != nil || got.PageSize != 7 || got.ReadinessDialAddress != "[::1]:9090" || got.ReadinessCAName != "issuer.pem" || got.BackupSource != r.BackupSource {
		t.Fatal(got, e)
	}
	for _, env := range []string{"PAGE_SIZE=7", "PROJECT_ONE_OFFLINE_CUMULATIVE_UPGRADE_PAGE_SIZE=false", "PROJECT_ONE_OFFLINE_CUMULATIVE_UPGRADE_PAGE_SIZE=0", "PROJECT_ONE_OFFLINE_CUMULATIVE_UPGRADE_PAGE_SIZE=01", "PROJECT_ONE_OFFLINE_CUMULATIVE_UPGRADE_OPERATION_SECONDS=31", "PROJECT_ONE_OFFLINE_CUMULATIVE_UPGRADE_READINESS_DIAL_ADDRESS=0.0.0.0:8080", "PROJECT_ONE_OFFLINE_CUMULATIVE_UPGRADE_READINESS_CA_NAME=../issuer.pem", "PROJECT_ONE_OFFLINE_CUMULATIVE_UPGRADE_BACKUP_MAXIMUM_ENTRIES=9999999999999999", "PROJECT_ONE_OFFLINE_CUMULATIVE_UPGRADE_PREPARED_ROOT=", "PROJECT_ONE_OFFLINE_CUMULATIVE_UPGRADE_PAGE_SIZE=8\nPROJECT_ONE_OFFLINE_CUMULATIVE_UPGRADE_PAGE_SIZE=8"} {
		if _, e := LoadCumulativeUpgradeRequest(raw, []byte(env)); e == nil {
			t.Fatal("invalid environment accepted", env)
		}
	}
	for _, bad := range []string{strings.Replace(string(raw), "prepared_root", "Bundle_file", 1), strings.Replace(string(raw), `"page_size":50`, `"page_size":null`, 1), strings.Replace(string(raw), `"page_size":50`, `"page_size":50,"page_size":50`, 1)} {
		if _, e := LoadCumulativeUpgradeRequest([]byte(bad), nil); e == nil {
			t.Fatal("ambiguous JSON accepted")
		}
	}
}

func TestMVPCumulativeUpgradeInventoryUsesExistingProtectedBoundary(t *testing.T) {
	r := CumulativeUpgradeDefaults()
	r.PreparedRoot = "/verified/prepared"
	r.ArchiveSHA256 = strings.Repeat("a", 64)
	r.ManifestSHA256 = strings.Repeat("b", 64)
	r.BackupSource.BackupRoot = "/verified/backup"
	r.BackupSource.ManifestSHA256 = strings.Repeat("c", 64)
	r.ReadinessDialAddress = "127.0.0.1:8080"
	original, _ := json.Marshal(r)
	if strings.Contains(string(original), "mvp_configuration_files") {
		t.Fatal("legacy encoding changed")
	}
	got, e := LoadCumulativeUpgradeRequest(original, []byte("PROJECT_ONE_OFFLINE_CUMULATIVE_UPGRADE_MVP_CONFIGURATION_FILES=/verified/private/mvp-files.json\n"))
	if e != nil || got.MVPConfigurationFiles != "/verified/private/mvp-files.json" {
		t.Fatal(got, e)
	}
	for _, value := range []string{"", "false", "relative", "/verified/../private/file"} {
		if _, e := LoadCumulativeUpgradeRequest(original, []byte("PROJECT_ONE_OFFLINE_CUMULATIVE_UPGRADE_MVP_CONFIGURATION_FILES="+value+"\n")); e == nil {
			t.Fatal("invalid reference", value)
		}
	}
	for _, value := range []string{`""`, `false`, `null`} {
		raw := strings.TrimSuffix(string(original), "}") + `,"mvp_configuration_files":` + value + `}`
		if _, e := LoadCumulativeUpgradeRequest([]byte(raw), nil); e == nil {
			t.Fatal("invalid inventory JSON", value)
		}
	}
}
