//go:build cgo && odbc_manager

package damengbackup

import (
	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/registryconfig"
)

// Config's ordinary JSON presentation intentionally omits private deployment
// references, including the native serving inputs and task bus. Bind the typed
// YAML projection plus its two separately loaded effective owners instead. This
// binds configuration values/references, not the bytes of referenced keyrings,
// certificates or provider credentials; that closure remains a separate owner.
func nativePlatformConfigurationSHA256(c config.Config) string {
	raw, e := yaml.Marshal(struct {
		Runtime       config.Config               `yaml:"runtime"`
		ImageRegistry *registryconfig.Config      `yaml:"effective_image_registry"`
		MongoDB       config.MongoDBConfiguration `yaml:"effective_mongodb"`
	}{c, c.ImageRegistry, c.MongoDB})
	if e != nil {
		return ""
	}
	return sha(raw)
}
