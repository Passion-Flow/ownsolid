package discovery

import (
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"
)

func parseOS(b []byte) (string, string, error) {
	if len(b) > 65536 || !utf8.Valid(b) {
		return "", "", errors.New("OS_RELEASE_INVALID")
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || (key != "ID" && key != "VERSION_ID") {
			continue
		}
		if _, exists := values[key]; exists {
			return "", "", errors.New("OS_RELEASE_INVALID")
		}
		if strings.HasPrefix(value, `"`) {
			v, e := strconv.Unquote(value)
			if e != nil {
				return "", "", errors.New("OS_RELEASE_INVALID")
			}
			value = v
		} else if strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") && len(value) >= 2 {
			value = value[1 : len(value)-1]
		}
		values[key] = value
	}
	if values["ID"] == "" || values["VERSION_ID"] == "" {
		return "", "", errors.New("OS_RELEASE_INCOMPLETE")
	}
	return values["ID"], values["VERSION_ID"], nil
}
func parseMemory(b []byte) (int64, error) {
	if len(b) > 1048576 || !utf8.Valid(b) {
		return 0, errors.New("MEMORY_DISCOVERY_INVALID")
	}
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "MemTotal:" {
			continue
		}
		if len(fields) != 3 || fields[2] != "kB" {
			break
		}
		kb, e := strconv.ParseInt(fields[1], 10, 64)
		if e != nil || kb < 1 || kb > 9007199254740991/1024 {
			break
		}
		return kb * 1024, nil
	}
	return 0, errors.New("MEMORY_DISCOVERY_INVALID")
}
