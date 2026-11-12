package install

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
	"go.yaml.in/yaml/v3"
)

func TestLicenseImportCapacitySurvivesMaterialization(t *testing.T) {
	installation := filepath.Join(shortTempDir(t), "installation")
	base := validYAML(t, installation)
	var retained map[string][]byte
	for step, maximum := range []int{128, 128, 256} {
		source := base + fmt.Sprintf(`
control_settings:
  runtime:
    licensing:
      maximum_imports: %d
    workspaces:
      maximum_count: 7
`, maximum)
		config, err := Decode([]byte(source))
		if err != nil {
			t.Fatal(err)
		}
		if err := Materialize(config); err != nil {
			t.Fatal(err)
		}
		layout := config.Layout()
		for _, name := range []string{filepath.Join(layout.Config, "control.yaml"), filepath.Join(layout.Operator, "operator.yaml")} {
			raw, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			var document struct {
				Runtime struct {
					Licensing  wire.Policy `yaml:"licensing"`
					Workspaces struct {
						MaximumCount int `yaml:"maximum_count"`
					} `yaml:"workspaces"`
				} `yaml:"runtime"`
				Licensing struct {
					TrustFile string `yaml:"trust_file"`
				} `yaml:"licensing"`
			}
			document.Runtime.Licensing = wire.DefaultPolicy()
			if err := yaml.Unmarshal(raw, &document); err != nil {
				t.Fatal(err)
			}
			if document.Runtime.Licensing.MaximumImports != maximum ||
				document.Runtime.Licensing.Validate() != nil ||
				document.Runtime.Workspaces.MaximumCount != 7 ||
				document.Licensing.TrustFile != containerConfigDir+"/license-trust.json" {
				t.Fatal("materialization lost the selected policy, another runtime setting or the owned trust reference")
			}
		}
		paths := []string{filepath.Join(layout.Config, "auth-hmac"), filepath.Join(layout.Secrets, "runtime-password"), filepath.Join(layout.Config, "license-trust.json")}
		if step == 0 {
			retained = map[string][]byte{}
			for _, name := range paths {
				raw, err := os.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				retained[name] = raw
			}
			// A generated-file edit cannot replace the authoritative installer input.
			if err := os.WriteFile(filepath.Join(layout.Config, "control.yaml"), []byte("runtime: {licensing: {maximum_imports: 1}}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		} else {
			for _, name := range paths {
				raw, err := os.ReadFile(name)
				if err != nil || !bytes.Equal(raw, retained[name]) {
					t.Fatal("reapplying a policy replaced retained authentication or trust material")
				}
			}
		}
	}
}
