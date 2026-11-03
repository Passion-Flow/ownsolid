package scope

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func TestMVPNativeConfigurationInventoryRetainsOriginalPrivateRoleBytes(t *testing.T) {
	// Real private files, synthetic bytes only; no signed/native source or
	// target key/policy admission is claimed by this component test.
	root := mvpNativeRestoreCustodyRoot(t)
	for _, name := range []string{"configuration", "configuration/control-config"} {
		if e := root.Mkdir(name, 0700); e != nil {
			t.Fatal(e)
		}
	}
	raw := []byte("Synthetic retained future key bytes\n")
	if e := writeBackupFile(root, "configuration/control-config/future-key", raw); e != nil {
		t.Fatal(e)
	}
	index := []BackupConfigurationFile{{Role: "control-config", File: InputFile{Name: "future-key", Bytes: int64(len(raw)), SHA256: wire.Digest(raw)}}}
	before, e := root.Stat("configuration/control-config/future-key")
	if e != nil {
		t.Fatal(e)
	}
	files, e := readMVPNativeConfigurationInventory(context.Background(), root, index, 1<<20)
	if e != nil || !bytes.Equal(files["control-config"][0].Contents, raw) {
		t.Fatal("original inventory bytes refused", e)
	}
	files["control-config"][0].Contents[0] = '!'
	again, e := readMVPNativeConfigurationInventory(context.Background(), root, index, 1<<20)
	after, se := root.Stat("configuration/control-config/future-key")
	if e != nil || se != nil || !os.SameFile(before, after) || before.ModTime() != after.ModTime() || !bytes.Equal(again["control-config"][0].Contents, raw) {
		t.Fatal("readonly reread changed original", e, se)
	}
}

func TestMVPNativeConfigurationInventoryRejectsChangedDescriptorsAndMissingBytes(t *testing.T) {
	for _, name := range []string{"role", "path", "digest", "size", "missing", "empty", "duplicate", "limit", "canceled"} {
		t.Run(name, func(t *testing.T) {
			root := mvpNativeRestoreCustodyRoot(t)
			if e := root.Mkdir("configuration", 0700); e != nil {
				t.Fatal(e)
			}
			if e := root.Mkdir("configuration/control-config", 0700); e != nil {
				t.Fatal(e)
			}
			raw := []byte("Synthetic original private configuration\n")
			if e := writeBackupFile(root, "configuration/control-config/runtime.yaml", raw); e != nil {
				t.Fatal(e)
			}
			index := []BackupConfigurationFile{{Role: "control-config", File: InputFile{Name: "runtime.yaml", Bytes: int64(len(raw)), SHA256: wire.Digest(raw)}}}
			ctx := context.Background()
			maximum := int64(1 << 20)
			switch name {
			case "role":
				index[0].Role = "unknown-config"
			case "path":
				index[0].File.Name = "../runtime.yaml"
			case "digest":
				index[0].File.SHA256 = strings.Repeat("0", 64)
			case "size":
				index[0].File.Bytes++
			case "missing":
				index[0].File.Name = "absent-key"
			case "empty":
				index = nil
			case "duplicate":
				index = append(index, index[0])
			case "limit":
				maximum = int64(len(raw) - 1)
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if files, e := readMVPNativeConfigurationInventory(ctx, root, index, maximum); e == nil || files != nil {
				t.Fatal("changed/missing original configuration accepted")
			}
		})
	}
}
