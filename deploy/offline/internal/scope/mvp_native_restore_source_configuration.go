package scope

import (
	"context"
	"os"
	"path"
	"reflect"
	"strings"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Read only the genuine native wrapper and its original protected role files.
// No legacy backup/job plan is constructed and no target policy is inferred.
func readMVPNativeRestoreSourceConfiguration(ctx context.Context, c configuration.Config, request configuration.RestoreSourceRequest, expected RestoreSourceResult) (mvpNativeBackupPlan, map[string][]engine.ConfigurationFile, error) {
	source, e := readMVPNativeRestoreSource(ctx, c, request)
	if e != nil {
		return mvpNativeBackupPlan{}, nil, e
	}
	if !validMVPNativeRestoreSource(expected) || !reflect.DeepEqual(source.Result, expected) {
		return mvpNativeBackupPlan{}, nil, failure("OFFLINE_SCOPE_MVP_NATIVE_SOURCE_CONFIGURATION_CONTEXT_CHANGED")
	}
	root, e := openBackupDirectory(request.BackupRoot)
	if e != nil {
		return mvpNativeBackupPlan{}, nil, e
	}
	defer root.Close()
	files, e := readMVPNativeConfigurationInventory(ctx, root, source.Plan.ConfigurationFiles, c.Verification.MaximumMetadataBytes)
	if e != nil {
		return mvpNativeBackupPlan{}, nil, e
	}
	return source.Plan, files, nil
}

// The source owner already validates the complete native source. This bounded
// private reread binds each requested role file to its original descriptor.
func readMVPNativeConfigurationInventory(ctx context.Context, root *os.Root, inventory []BackupConfigurationFile, maximum int64) (map[string][]engine.ConfigurationFile, error) {
	bad := failure("OFFLINE_SCOPE_MVP_NATIVE_SOURCE_CONFIGURATION_CHANGED")
	if ctx == nil || ctx.Err() != nil || root == nil || maximum < 1 || len(inventory) < 1 || len(inventory) > 1024 {
		return nil, bad
	}
	files := map[string][]engine.ConfigurationFile{}
	seen := map[string]bool{}
	for _, item := range inventory {
		name := item.Role + "/" + item.File.Name
		if ctx.Err() != nil || roleServices[item.Role] == "" || !strings.HasSuffix(item.Role, "-config") || !wire.SafePath(item.File.Name) || path.Base(item.File.Name) != item.File.Name || item.File.Bytes < 1 || item.File.Bytes > maximum || !wire.ValidSHA256(item.File.SHA256) || seen[name] {
			return nil, bad
		}
		seen[name] = true
		raw, e := regular(root, "configuration/"+name, maximum)
		if e != nil || int64(len(raw)) != item.File.Bytes || wire.Digest(raw) != item.File.SHA256 {
			return nil, bad
		}
		files[item.Role] = append(files[item.Role], engine.ConfigurationFile{Name: item.File.Name, Contents: raw})
	}
	return files, nil
}
