package factory

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

func TestFixedReserveCapacityAndLegacyBoundary(t *testing.T) {
	const gib = uint64(1 << 30)
	fixed := Config{MinimumFreeBytes: 32 << 30}
	if hasConfiguredHeadroom(Config{}, 1024*gib, 300*gib, 32<<30) || !hasConfiguredHeadroom(fixed, 1024*gib, 300*gib, 32<<30) {
		t.Fatal("explicit fixed reserve must not add the historical percentage floor")
	}
	for _, v := range []struct {
		total, available uint64
		additional       int64
		want             bool
	}{
		{1024 * gib, 64 * gib, 32 << 30, true},
		{1024 * gib, 64*gib - 1, 32 << 30, false},
		{1024 * gib, 32 * gib, 0, true},
		{1024 * gib, 32*gib - 1, 0, false},
		{1024 * gib, 32 * gib, -1, false},
		{0, 64 * gib, 0, false},
		{32 * gib, 64 * gib, 0, false},
		{^uint64(0), ^uint64(0), 1 << 62, true},
	} {
		if got := hasConfiguredHeadroom(fixed, v.total, v.available, v.additional); got != v.want {
			t.Fatal(v, got)
		}
	}
	for _, n := range []int64{-1, 1<<50 + 1} {
		if hasConfiguredHeadroom(Config{MinimumFreeBytes: n}, 1024*gib, 300*gib, 0) {
			t.Fatal("invalid direct reserve admitted", n)
		}
	}
}

func TestFixedReserveStrictProtectedConfigurationAndExplicitEnvironment(t *testing.T) {
	t.Setenv("PROJECT_ONE_OFFLINE_FACTORY_MINIMUM_FREE_BYTES", "-1")
	c, e := LoadConfig([]byte(`{"minimum_free_bytes":34359738368,"allow_test_fixture":true}`), []byte("PROJECT_ONE_OFFLINE_FACTORY_MINIMUM_FREE_BYTES=68719476736\nPROJECT_ONE_OFFLINE_FACTORY_ALLOW_TEST_FIXTURE=false\n"))
	if e != nil || c.MinimumFreeBytes != 64<<30 || c.AllowTestFixture {
		t.Fatal("explicit mapping/false/ambient isolation", e)
	}
	legacy, e := LoadConfig([]byte(`{}`), nil)
	if e != nil || legacy.MinimumFreeBytes != 0 || bytes.Contains(Encode(legacy), []byte("minimum_free_bytes")) {
		t.Fatal("legacy canonical configuration bytes changed", e)
	}
	for _, raw := range []string{`null`, `{"minimum_free_bytes":null}`, `{"minimum_free_bytes":true}`, `{"minimum_free_bytes":"32"}`, `{"minimum_free_bytes":-1}`, `{"minimum_free_bytes":1125899906842625}`, `{"minimum_free_bytes":1.5}`, `{"minimum_free_bytes":0,"minimum_free_bytes":1}`, `{"Minimum_Free_Bytes":1}`, "{\"minimum_free_byte\u017f\":1}", `{"allow_test_fixture":null}`, `{"Allow_Test_Fixture":true}`, `{"unknown":1}`} {
		if _, e := LoadConfig([]byte(raw), nil); e == nil {
			t.Fatal("invalid scalar/config accepted", raw)
		}
	}
	for _, value := range []string{"", "-1", "1125899906842625", "true", "1.5", "1\nPROJECT_ONE_OFFLINE_FACTORY_MINIMUM_FREE_BYTES=2"} {
		if _, e := LoadConfig([]byte(`{}`), []byte("PROJECT_ONE_OFFLINE_FACTORY_MINIMUM_FREE_BYTES="+value)); e == nil {
			t.Fatal("invalid explicit environment accepted", value)
		}
	}
	if _, e := LoadConfig([]byte("{\"mount_root\":\"\xff\"}"), nil); e == nil {
		t.Fatal("invalid UTF8 accepted")
	}
}

func TestFixedReserveRealBuildInspectAndUnpackKeepOriginalAdmission(t *testing.T) {
	c, _, _ := setup(t)
	c.MinimumFreeBytes = 1 << 50
	if _, e := Build(context.Background(), c); e == nil || e.Error() != "RELEASE_CAPACITY_HEADROOM_REQUIRED" {
		t.Fatal("reserve refusal", e)
	}
	for _, suffix := range []string{"", ".incomplete", ".journal.jsonl"} {
		if _, e := os.Stat(c.OutputFile + suffix); !os.IsNotExist(e) {
			t.Fatal("capacity refusal published output", suffix, e)
		}
	}
	c.MinimumFreeBytes = 32 << 20
	built, e := Build(context.Background(), c)
	if e != nil {
		t.Fatal("fixed reserve build", e)
	}
	checked, e := InspectExpected(context.Background(), c, c.OutputFile, built.ArchiveSHA256, built.ManifestSHA256)
	if e != nil || checked.ManifestSHA256 != built.ManifestSHA256 {
		t.Fatal("independent all-byte inspect", e)
	}
	destination := strings.TrimSuffix(c.OutputFile, ".tar.gz") + "-prepared"
	c.MinimumFreeBytes = 1 << 50
	if _, e = Unpack(context.Background(), c, c.OutputFile, destination, built.ArchiveSHA256, built.ManifestSHA256); e == nil || e.Error() != "RELEASE_CAPACITY_HEADROOM_REQUIRED" {
		t.Fatal("unpack lost selected policy", e)
	}
	if _, e = os.Stat(destination); !os.IsNotExist(e) {
		t.Fatal("unpack reserve refusal created target", e)
	}
	c.MinimumFreeBytes = 32 << 20
	if _, e = Unpack(context.Background(), c, c.OutputFile, destination, built.ArchiveSHA256, built.ManifestSHA256); e != nil {
		t.Fatal("fixed reserve preparation", e)
	}
	if _, e = VerifyPrepared(context.Background(), c, destination, built.ArchiveSHA256, built.ManifestSHA256); e != nil {
		t.Fatal("fixed reserve prepared readback", e)
	}
	c.MountRoot = "/"
	if _, e = Build(context.Background(), c); e == nil || e.Error() != "RELEASE_MOUNTED_PRIVATE_INPUT_REQUIRED" {
		t.Fatal("fixed reserve bypassed genuine mount")
	}
}
