package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const configName = "config.json"

var (
	topologyID = regexp.MustCompile("^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$")
	resourceID = regexp.MustCompile("^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$")
	hostLabel  = regexp.MustCompile("^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$")
)

type fileConfig struct {
	Server      string `json:"server"`
	SiteID      string `json:"site_id"`
	CellID      string `json:"cell_id"`
	WorkspaceID string `json:"workspace_id"`
	CAFile      string `json:"ca_file"`
	Timeout     string `json:"request_timeout"`
}

type settings struct {
	dir     string
	origin  url.URL
	timeout time.Duration
	fileConfig
}

func configDir() (string, error) {
	if dir := os.Getenv("OWNSOLID_CONFIG_DIR"); dir != "" {
		if !filepath.IsAbs(dir) {
			return "", errors.New("OWNSOLID_CONFIG_DIR must be an absolute path")
		}
		return filepath.Clean(dir), nil
	}
	if dir := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(dir) {
		return filepath.Join(dir, "ownsolid"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("cannot find the home directory; set OWNSOLID_CONFIG_DIR")
	}
	return filepath.Join(home, ".config", "ownsolid"), nil
}

func loadSettings(server string, serverSet bool) (settings, error) {
	dir, err := configDir()
	if err != nil {
		return settings{}, err
	}
	cfg := settings{
		dir:        dir,
		timeout:    30 * time.Second,
		fileConfig: fileConfig{SiteID: "default", CellID: "default"},
	}
	if err := readConfig(filepath.Join(dir, configName), &cfg.fileConfig); err != nil {
		return settings{}, err
	}
	if serverSet {
		cfg.Server = server
	} else if env := os.Getenv("OWNSOLID_SERVER"); env != "" {
		cfg.Server = env
	}
	if cfg.Server == "" {
		return settings{}, errors.New("set the Admin origin with --server, OWNSOLID_SERVER, or server in config.json")
	}
	cfg.origin, err = parseOrigin(cfg.Server)
	if err != nil {
		return settings{}, err
	}
	if !topologyID.MatchString(cfg.SiteID) || !topologyID.MatchString(cfg.CellID) {
		return settings{}, errors.New("site_id and cell_id must be lowercase topology identifiers in config.json")
	}
	if cfg.WorkspaceID != "" && !resourceID.MatchString(cfg.WorkspaceID) {
		return settings{}, errors.New("workspace_id in config.json must be a UUID")
	}
	if cfg.CAFile != "" && !filepath.IsAbs(cfg.CAFile) {
		return settings{}, errors.New("ca_file in config.json must be an absolute path")
	}
	if cfg.Timeout != "" {
		cfg.timeout, err = time.ParseDuration(cfg.Timeout)
		if err != nil || cfg.timeout <= 0 {
			return settings{}, errors.New("request_timeout in config.json must be a positive Go duration, such as 30s")
		}
	}
	return cfg, nil
}

func readConfig(path string, config *fileConfig) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open config file %q: %w", path, err)
	}
	defer f.Close()
	const maxConfigBytes = 64 << 10
	raw, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if err != nil {
		return fmt.Errorf("read config file %q: %w", path, err)
	}
	if len(raw) > maxConfigBytes {
		return fmt.Errorf("config file %q exceeds 64 KiB", path)
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil || config == nil || decoder.Decode(new(any)) != io.EOF {
		return fmt.Errorf("config file %q must contain one JSON object with supported settings; see the CLI README", path)
	}
	return nil
}

func parseOrigin(value string) (url.URL, error) {
	invalid := errors.New("server must be an Admin HTTP or HTTPS origin with no credentials, path, query, or fragment")
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "http" && u.Scheme != "https" || u.Host == "" ||
		u.User != nil || u.Opaque != "" || (u.Path != "" && u.Path != "/") ||
		u.RawPath != "" || strings.ContainsAny(value, "%?#\\") {
		return url.URL{}, invalid
	}
	host := strings.ToLower(u.Hostname())
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	} else {
		if len(host) > 253 {
			return url.URL{}, invalid
		}
		for _, label := range strings.Split(host, ".") {
			if !hostLabel.MatchString(label) {
				return url.URL{}, invalid
			}
		}
	}
	if strings.HasSuffix(u.Host, ":") {
		return url.URL{}, invalid
	}
	authority := host
	if strings.Contains(host, ":") {
		authority = "[" + host + "]"
	}
	if port := u.Port(); port != "" {
		n, err := strconv.ParseUint(port, 10, 16)
		if err != nil || n == 0 {
			return url.URL{}, invalid
		}
		if !(u.Scheme == "http" && n == 80 || u.Scheme == "https" && n == 443) {
			authority = net.JoinHostPort(host, strconv.FormatUint(n, 10))
		}
	}
	return url.URL{Scheme: u.Scheme, Host: authority}, nil
}
