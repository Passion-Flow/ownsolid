package install

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// These public fields follow api/configuration/v1/control-serving-expectation.
// The Control command owns configuration and TLS validation, including machine TLS.
type servingExpectation struct {
	FormatVersion      int      `json:"format_version"`
	TransportMode      string   `json:"transport_mode"`
	ListenAddress      string   `json:"listen_address"`
	PortalOrigin       string   `json:"portal_origin"`
	AdminOrigin        string   `json:"admin_origin"`
	TLSCertificateFile string   `json:"tls_certificate_file"`
	TLSPrivateKeyFile  string   `json:"tls_private_key_file"`
	TrustedProxyCIDRs  []string `json:"trusted_proxy_cidrs"`
}

const servingTransportSuccess = "CONTROL_SERVING_TRANSPORT_OK"

func (c Config) servingArguments(configPath string) ([]string, error) {
	public := servingExpectation{
		FormatVersion: 1, TransportMode: "direct_https", ListenAddress: containerListen,
		PortalOrigin: c.Origins.Portal, AdminOrigin: c.Origins.Admin,
		TLSCertificateFile: containerConfigDir + "/certificate.pem",
		TLSPrivateKeyFile:  containerConfigDir + "/private-key.pem", TrustedProxyCIDRs: []string{},
	}
	raw, err := json.Marshal(public)
	if err != nil {
		return nil, errors.New("INSTALL_SERVING_INTENT_INVALID: cannot encode public serving intent")
	}
	args := []string{"verify-serving-transport", "--config", configPath, "--expected-server-json", string(raw)}
	machine, err := c.machineServingIntent()
	if err != nil {
		return nil, err
	}
	if machine != nil {
		raw, err = json.Marshal(machine)
		if err != nil {
			return nil, errors.New("INSTALL_SERVING_INTENT_INVALID: cannot encode public machine intent")
		}
		args = append(args, "--expected-machine-json", string(raw))
	}
	return args, nil
}

func (c Config) machineServingIntent() (map[string]string, error) {
	value, present := c.ControlSettings["machine_server"]
	if !present {
		return nil, nil
	}
	fields, ok := value.(map[string]any)
	if !ok || len(fields) != 4 {
		return nil, invalid("control_settings.machine_server", "must contain exactly the four public machine-server fields")
	}
	intent := make(map[string]string, 4)
	for _, name := range []string{"listen_address", "origin", "tls_certificate_file", "tls_private_key_file"} {
		text, ok := fields[name].(string)
		if !ok || text == "" {
			return nil, invalid("control_settings.machine_server."+name, "must be an explicit nonempty string")
		}
		intent[name] = text
	}
	return intent, nil
}

func (i *installer) servingTransport(ctx context.Context) error {
	args, err := i.config.servingArguments(i.runtimeConfig())
	if err != nil {
		return err
	}
	_, err = i.operator(ctx, servingTransportSuccess, args...)
	return err
}

// verifyServingImage checks an admitted image against the installed configuration
// before an upgrade or rollback stops the writer. The override changes only the
// operator image; installed files and the serving container remain untouched.
func (i *installer) verifyServingImage(ctx context.Context, image string) (err error) {
	args, err := i.config.servingArguments(i.runtimeConfig())
	if err != nil {
		return err
	}
	digest, pinned := strings.CutPrefix(image, "sha256:")
	if !pinned || !wire.ValidSHA256(digest) {
		return errors.New("INSTALL_SERVING_IMAGE_INVALID: supply the admitted immutable Control image")
	}
	override, err := os.CreateTemp(i.config.Layout().Operator, "serving-image-*.json")
	if err != nil {
		return errors.New("INSTALL_SERVING_CHECK_UNAVAILABLE: cannot create the private image override")
	}
	path := override.Name()
	defer func() {
		if removeErr := os.Remove(path); removeErr != nil {
			err = errors.Join(err, errors.New("INSTALL_SERVING_CHECK_CLEANUP_FAILED: cannot remove the private image override"))
		}
	}()
	document := map[string]any{"services": map[string]any{"control-operator": map[string]string{"image": image}}}
	err = json.NewEncoder(override).Encode(document)
	err = errors.Join(err, override.Close())
	if err != nil {
		return errors.New("INSTALL_SERVING_CHECK_UNAVAILABLE: cannot write the private image override")
	}
	command := append([]string{"-f", filepath.Clean(path), "--profile", "operator", "run", "--rm", "--no-deps", "--no-TTY", "control-operator"}, args...)
	output, runErr := i.compose(ctx, command...)
	if runErr != nil || !containsLine(output, servingTransportSuccess) {
		return &StepError{Code: "INSTALL_SERVING_TRANSPORT_REFUSED", Detail: "the admitted image rejected the installed serving intent; keep the active version and correct the configuration before retrying: " + lastLines(output)}
	}
	return nil
}
