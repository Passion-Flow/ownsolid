package engine

import (
	"strings"
	"testing"
)

func TestNativeConfigurationMountBoundary(t *testing.T) {
	raw := "1 0 0:1 / / rw - overlay overlay rw\n2 1 0:2 / /source ro - virtiofs source ro\n3 1 0:3 /owned /target rw - ext4 /dev/vdb rw\n"
	if e := validateNativeConfigurationMounts(raw, "/source", "/target", "install"); e != nil {
		t.Fatal(e)
	}
	for _, changed := range []string{strings.Replace(raw, "/source ro", "/source rw", 1), strings.Replace(raw, "/target rw", "/target ro", 1), strings.Replace(raw, "ext4", "tmpfs", 1), raw + "4 3 0:4 / /target/private rw - ext4 /dev/vdc rw\n", raw + "4 1 0:4 / /target rw - ext4 /dev/vdc rw\n", strings.Replace(raw, "/target", "/wrong", 1)} {
		if e := validateNativeConfigurationMounts(changed, "/source", "/target", "install"); e == nil {
			t.Fatal("accepted invalid mounts")
		}
	}
	withoutSource := "3 1 0:3 /owned /target ro - ext4 /dev/vdb rw\n"
	if e := validateNativeConfigurationMounts(withoutSource, "/source", "/target", "inspect"); e != nil {
		t.Fatal(e)
	}
	if e := validateNativeConfigurationMounts(withoutSource, "/source", "/target", "recover"); e == nil {
		t.Fatal("recovery accepted readonly volume")
	}
}
