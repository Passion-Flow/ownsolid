package engine

import (
	"archive/tar"
	"bytes"
	"io"
	"testing"
)

func TestConfigurationArchiveRejectsUnknownTypesOwnersContentAndTrailingData(t *testing.T) {
	c, _, _, _ := volumeClient(t, "")
	files := []ConfigurationFile{{Name: "control.yaml", Contents: []byte("Explicit test configuration")}}
	input, e := c.configurationTar(files, 10033, 10034)
	if e != nil {
		t.Fatal(e)
	}
	for _, fault := range []string{"none", "root-owner", "private-mode", "file-owner", "file-mode", "symlink", "hash", "extra", "truncated", "trailing"} {
		t.Run(fault, func(t *testing.T) {
			var output bytes.Buffer
			writer := tar.NewWriter(&output)
			root := &tar.Header{Name: "volume-init", Typeflag: tar.TypeDir, Mode: 0755}
			if fault == "root-owner" {
				root.Uid = 10033
			}
			writer.WriteHeader(root)
			reader := tar.NewReader(bytes.NewReader(input))
			for {
				header, e := reader.Next()
				if e == io.EOF {
					break
				}
				if e != nil {
					t.Fatal(e)
				}
				copy := *header
				copy.Name = "volume-init/" + header.Name
				data, _ := io.ReadAll(reader)
				if copy.Typeflag == tar.TypeDir && fault == "private-mode" {
					copy.Mode = 0755
				}
				if copy.Typeflag == tar.TypeReg {
					switch fault {
					case "file-owner":
						copy.Uid = 0
					case "file-mode":
						copy.Mode = 0644
					case "symlink":
						copy.Typeflag = tar.TypeSymlink
						copy.Linkname = "/foreign"
						copy.Size = 0
						data = nil
					case "hash":
						data[0] ^= 1
					}
				}
				if e = writer.WriteHeader(&copy); e != nil {
					t.Fatal(e)
				}
				if len(data) > 0 {
					writer.Write(data)
				}
			}
			if fault == "extra" {
				writer.WriteHeader(&tar.Header{Name: "volume-init/private/unexpected", Typeflag: tar.TypeReg, Mode: 0600, Uid: 10033, Gid: 10034, Size: 1})
				writer.Write([]byte("x"))
			}
			writer.Close()
			raw := output.Bytes()
			if fault == "truncated" {
				raw = raw[:512+100]
			}
			if fault == "trailing" {
				raw = append(raw, []byte("undeclared second payload")...)
			}
			result, e := inspectConfigurationArchive(raw, files, 10033, 10034)
			if fault == "none" {
				if e != nil || result.Status != "verified_private_volume_payload" {
					t.Fatal(result, e)
				}
			} else if e == nil {
				t.Fatal("unsafe archive accepted", fault)
			}
		})
	}
}
