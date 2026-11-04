package release

import (
	"reflect"
	"testing"
)

func TestSingleProfileRequiresTheTargetProbeImage(t *testing.T) {
	m := mvpManifestDouble()
	if e := m.Validate(100, 100, 10000, false); e != nil {
		t.Fatal(e)
	}
	for i, asset := range m.Assets {
		if asset.ID == PrivateClusterProbeAMD64AssetID {
			m.Assets = append(m.Assets[:i:i], m.Assets[i+1:]...)
			break
		}
	}
	if e := m.Validate(100, 100, 10000, false); e == nil {
		t.Fatal("release without the probe image admitted")
	}
}

func TestProbeImageArchitectureAndRequiredMapIsolation(t *testing.T) {
	if validImageArchitecture(Asset{ID: PrivateClusterProbeARM64AssetID, Architecture: "amd64"}) || !validImageArchitecture(Asset{ID: PrivateClusterProbeARM64AssetID, Architecture: "arm64"}) {
		t.Fatal("local role identity does not match architecture")
	}
	before := RequiredAssets(MVPPostgreSQLCPUProfile)
	actual := mvpManifestDouble().RequiredAssets()
	delete(actual, "control-image")
	actual["foreign"] = "operator"
	if !reflect.DeepEqual(before, RequiredAssets(MVPPostgreSQLCPUProfile)) {
		t.Fatal("mutable requirement map leaked into a different release")
	}
	for _, architecture := range []string{"arm64", "", "riscv64"} {
		m := mvpManifestDouble()
		for i := range m.Assets {
			if m.Assets[i].ID == PrivateClusterProbeAMD64AssetID {
				m.Assets[i].Architecture = architecture
			}
		}
		if e := m.Validate(100, 100, 10000, false); e == nil {
			t.Fatal("wrong target image architecture admitted", architecture)
		}
	}
}
