package damengbackup

import (
	"encoding/json"
	"github.com/google/uuid"
	"os"
	"strings"
	"unicode/utf8"
)

const OperatorRequestEnvironment = "PROJECT_ONE_DAMENG_RECOVERY_REQUEST_FILE"
const BackupCommand = "backup-native-dameng"
const RestoreCommand = "restore-native-dameng"
const VerifyBackupCommand = "verify-native-dameng-backup"
const VerifyRestoreCommand = "verify-native-dameng-restore"

type ImageRootBinding struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// OperatorRequest is one original operator intent. Owner input files are kept
// separate from the application projection; no request carries secret values.
type OperatorRequest struct {
	FormatVersion                int                `json:"format_version"`
	OperationID                  string             `json:"operation_id"`
	Kind                         string             `json:"kind"`
	Reason                       string             `json:"reason"`
	ApplicationConfigurationFile string             `json:"application_configuration_file"`
	ApplicationEnvironmentFile   string             `json:"application_environment_file"`
	RecoveryConfigurationFile    string             `json:"recovery_configuration_file"`
	RecoveryEnvironmentFile      string             `json:"recovery_environment_file"`
	NativeContextFile            string             `json:"native_context_file"`
	NativeEnvironmentFile        string             `json:"native_environment_file"`
	ConfigurationRoot            string             `json:"configuration_root"`
	OutputRoot                   string             `json:"output_root"`
	SourceRoot                   string             `json:"source_root"`
	ConfirmFreshTarget           bool               `json:"confirm_fresh_target"`
	ImageRoots                   []ImageRootBinding `json:"image_roots"`
}

func (OperatorRequest) String() string   { return "DAMENG_OPERATOR_REQUEST_REDACTED" }
func (OperatorRequest) GoString() string { return "DAMENG_OPERATOR_REQUEST_REDACTED" }
func OperatorCommand(command string) bool {
	return command == BackupCommand || command == RestoreCommand || command == VerifyBackupCommand || command == VerifyRestoreCommand
}
func (r OperatorRequest) Validate() error {
	id, e := uuid.Parse(r.OperationID)
	if e != nil || id == uuid.Nil || id.String() != r.OperationID || r.FormatVersion != 1 || (r.Kind != "backup" && r.Kind != "restore") || !utf8.ValidString(r.Reason) || strings.TrimSpace(r.Reason) == "" || len(r.Reason) > 500 || strings.ContainsAny(r.Reason, "\x00\r\n") || !canonical(r.ConfigurationRoot) || !canonical(r.OutputRoot) || related(r.ConfigurationRoot, r.OutputRoot) || r.ImageRoots == nil || len(r.ImageRoots) > 32 {
		return fail("OPERATOR_REQUEST_INVALID")
	}
	for _, p := range []string{r.ApplicationConfigurationFile, r.RecoveryConfigurationFile, r.NativeContextFile} {
		if !canonical(p) || related(r.OutputRoot, p) {
			return fail("OPERATOR_REQUEST_INVALID")
		}
	}
	for _, p := range []string{r.ApplicationEnvironmentFile, r.RecoveryEnvironmentFile, r.NativeEnvironmentFile} {
		if p != "" && (!canonical(p) || related(r.OutputRoot, p)) {
			return fail("OPERATOR_REQUEST_INVALID")
		}
	}
	if r.Kind == "backup" && (r.SourceRoot != "" || r.ConfirmFreshTarget) || r.Kind == "restore" && (!r.ConfirmFreshTarget || !canonical(r.SourceRoot) || related(r.SourceRoot, r.OutputRoot) || related(r.SourceRoot, r.ConfigurationRoot)) {
		return fail("OPERATOR_REQUEST_INVALID")
	}
	if !withinOperatorConfiguration(r.ConfigurationRoot, r.ApplicationConfigurationFile) || r.ApplicationEnvironmentFile != "" && !withinOperatorConfiguration(r.ConfigurationRoot, r.ApplicationEnvironmentFile) {
		return fail("OPERATOR_APPLICATION_OUTSIDE_CONFIGURATION")
	}
	seen := map[string]bool{}
	for _, image := range r.ImageRoots {
		if !canonical(image.Path) || !digest.MatchString(image.SHA256) || seen[image.Path] || related(image.Path, r.ConfigurationRoot) || related(image.Path, r.OutputRoot) {
			return fail("OPERATOR_IMAGE_BINDING_INVALID")
		}
		seen[image.Path] = true
	}
	return nil
}
func LoadOperatorRequest(path, environmentFile string) (OperatorRequest, error) {
	load := func(path string) (OperatorRequest, error) {
		var r OperatorRequest
		raw, e := readPrivate(path, maximumDocumentBytes)
		var fields map[string]json.RawMessage
		if e != nil || strictJSON(raw, &r) != nil || json.Unmarshal(raw, &fields) != nil || len(fields) != 15 || r.Validate() != nil {
			return r, fail("OPERATOR_REQUEST_INVALID")
		}
		return r, nil
	}
	r, e := load(path)
	if e != nil || environmentFile == "" {
		return r, e
	}
	raw, e := readPrivate(environmentFile, maximumDocumentBytes)
	if e != nil {
		return OperatorRequest{}, fail("OPERATOR_ENVIRONMENT_INVALID")
	}
	replacement := ""
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || k != OperatorRequestEnvironment || replacement != "" || !canonical(v) {
			return OperatorRequest{}, fail("OPERATOR_ENVIRONMENT_INVALID")
		}
		replacement = v
	}
	if replacement == "" {
		return OperatorRequest{}, fail("OPERATOR_ENVIRONMENT_INVALID")
	}
	return load(replacement)
}

// Private operator YAML uses its established decoder after the exact protected
// file protocol, never config.Load/os.ReadFile or an ambient environment.
func ReadOperatorApplication(path string) ([]byte, error) { return readPrivate(path, 256<<10) }
func OperatorRequestPath(path string) bool                { return canonical(path) }

// No ambient native loader override may enter an operator invocation.
func RefuseOperatorAmbient() error {
	for _, k := range []string{"LD_PRELOAD"} {
		if _, ok := os.LookupEnv(k); ok {
			return fail("OPERATOR_AMBIENT_OVERRIDE_REFUSED")
		}
	}
	return nil
}

func withinOperatorConfiguration(root, path string) bool { return strings.HasPrefix(path, root+"/") }
