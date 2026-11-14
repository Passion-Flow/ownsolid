// Package mysqlstore owns the real MySQL connection/transaction boundary.
// Product repository and serving support require separate explicit integration.
package mysqlstore

import (
	_ "embed"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

//go:embed defaults.json
var defaultPolicyJSON []byte

type Policy struct {
	MaximumOpenConnections int `json:"maximum_open_connections,omitempty"`
	MaximumIdleConnections int `json:"maximum_idle_connections,omitempty"`
	ConnectSeconds         int `json:"connect_seconds,omitempty"`
	ReadSeconds            int `json:"read_seconds,omitempty"`
	WriteSeconds           int `json:"write_seconds,omitempty"`
	StatementSeconds       int `json:"statement_seconds,omitempty"`
	TransactionSeconds     int `json:"transaction_seconds,omitempty"`
	LockSeconds            int `json:"lock_seconds,omitempty"`
	MaximumLifetimeSeconds int `json:"maximum_lifetime_seconds,omitempty"`
	MaximumIdleSeconds     int `json:"maximum_idle_seconds,omitempty"`
	MaximumPacketBytes     int `json:"maximum_packet_bytes,omitempty"`
}

type Configuration struct {
	Host            string `json:"host"`
	Port            int    `json:"port"`
	Database        string `json:"database"`
	User            string `json:"user"`
	PasswordFile    string `json:"password_file"`
	CAFile          string `json:"ca_file"`
	ServerName      string `json:"server_name"`
	ExpectedVersion string `json:"expected_version"`
	Policy          Policy `json:"policy,omitempty"`
}

// Input fields may omit an inherited default, but a resolved zero-idle policy
// must survive serialization into an operator projection or restart snapshot.
func (p Policy) MarshalJSON() ([]byte, error) {
	fields := map[string]int{}
	value := reflect.ValueOf(p)
	kind := value.Type()
	for i := 0; i < value.NumField(); i++ {
		fields[strings.Split(kind.Field(i).Tag.Get("json"), ",")[0]] = int(value.Field(i).Int())
	}
	return json.Marshal(fields)
}

func DefaultPolicy() Policy {
	var p Policy
	if json.Unmarshal(defaultPolicyJSON, &p) != nil {
		panic("MYSQL_OWNED_POLICY_INVALID")
	}
	return p
}

func (p Policy) Validate() error {
	if p.MaximumOpenConnections < 1 || p.MaximumOpenConnections > 32 || p.MaximumIdleConnections < 0 || p.MaximumIdleConnections > p.MaximumOpenConnections || p.ConnectSeconds < 1 || p.ConnectSeconds > 60 || p.ReadSeconds < 1 || p.ReadSeconds > 300 || p.WriteSeconds < 1 || p.WriteSeconds > 300 || p.StatementSeconds < 1 || p.StatementSeconds > 300 || p.TransactionSeconds < 1 || p.TransactionSeconds > 300 || p.LockSeconds < 1 || p.LockSeconds > 60 || p.MaximumLifetimeSeconds < 1 || p.MaximumLifetimeSeconds > 86400 || p.MaximumIdleSeconds < 1 || p.MaximumIdleSeconds > 86400 || p.MaximumPacketBytes < 1048576 || p.MaximumPacketBytes > 67108864 {
		return errors.New("MYSQL_POLICY_INVALID")
	}
	return nil
}

var databaseName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
var userName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,31}$`)
var supportedVersion = regexp.MustCompile(`^8\.4\.(0|[1-9][0-9]*)$`)
var dnsName = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)

func validHost(value string) bool {
	if value == "" || len(value) > 253 {
		return false
	}
	if address := net.ParseIP(value); address != nil {
		return !address.IsUnspecified() && !address.IsMulticast()
	}
	return dnsName.MatchString(value)
}
func privatePath(value string) bool {
	return value != "" && filepath.IsAbs(value) && filepath.Clean(value) == value && filepath.Base(value) != string(filepath.Separator) && !strings.ContainsAny(value, "\x00\r\n")
}

func (c Configuration) Validate() error {
	if !validHost(c.Host) || !validHost(c.ServerName) || c.Port < 1 || c.Port > 65535 || !databaseName.MatchString(c.Database) || !userName.MatchString(c.User) || !privatePath(c.PasswordFile) || !privatePath(c.CAFile) || c.PasswordFile == c.CAFile || !supportedVersion.MatchString(c.ExpectedVersion) {
		return errors.New("MYSQL_CONFIGURATION_INVALID")
	}
	return c.Policy.Validate()
}

// Decode uses no ambient environment and never accepts a free-form DSN/driver flags.
func Decode(raw, environment []byte) (Configuration, error) {
	c := Configuration{Policy: DefaultPolicy()}
	if strict.DecodeStrict(raw, &c) != nil || !utf8.Valid(environment) || strings.ContainsAny(string(environment), "\x00\r") {
		return Configuration{}, errors.New("MYSQL_CONFIGURATION_INVALID")
	}
	fields := map[string]reflect.Value{}
	for _, object := range []any{&c, &c.Policy} {
		value := reflect.ValueOf(object).Elem()
		kind := value.Type()
		for i := 0; i < value.NumField(); i++ {
			field := value.Field(i)
			if field.Kind() != reflect.String && field.Kind() != reflect.Int {
				continue
			}
			name := strings.Split(kind.Field(i).Tag.Get("json"), ",")[0]
			fields["PROJECT_ONE_MYSQL_"+strings.ToUpper(name)] = field
		}
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(environment), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		field, known := fields[name]
		if !ok || !known || seen[name] || value == "" {
			return Configuration{}, errors.New("MYSQL_ENVIRONMENT_INVALID")
		}
		seen[name] = true
		if strings.HasPrefix(value, `"`) {
			var decoded string
			if json.Unmarshal([]byte(value), &decoded) != nil {
				return Configuration{}, errors.New("MYSQL_ENVIRONMENT_INVALID")
			}
			value = decoded
		} else if strings.HasPrefix(value, "'") {
			if len(value) < 2 || !strings.HasSuffix(value, "'") {
				return Configuration{}, errors.New("MYSQL_ENVIRONMENT_INVALID")
			}
			value = value[1 : len(value)-1]
		}
		if value == "" || strings.ContainsAny(value, "\x00\r\n$`") {
			return Configuration{}, errors.New("MYSQL_ENVIRONMENT_INVALID")
		}
		if field.Kind() == reflect.String {
			field.SetString(value)
		} else {
			n, e := strconv.ParseInt(value, 10, 32)
			if e != nil || strconv.FormatInt(n, 10) != value {
				return Configuration{}, errors.New("MYSQL_ENVIRONMENT_INVALID")
			}
			field.SetInt(n)
		}
	}
	if e := c.Validate(); e != nil {
		return Configuration{}, e
	}
	return c, nil
}

func Load(path, environmentFile string) (Configuration, error) {
	raw, e := privateInput(path, MaximumConfigurationBytes)
	if e != nil {
		return Configuration{}, e
	}
	var environment []byte
	if environmentFile != "" {
		environment, e = privateInput(environmentFile, MaximumConfigurationBytes)
		if e != nil {
			return Configuration{}, e
		}
	}
	return Decode(raw, environment)
}
