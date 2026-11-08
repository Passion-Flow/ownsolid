// Package productagent resolves model-independent commands under the caller's session.
package productagent

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxCommandBytes bounds the command language at its input boundary.
const MaxCommandBytes = 128

type command struct {
	id     string
	admin  string
	portal string
}

const (
	listApplications  = "list_applications"
	listConversations = "list_conversations"
)

var commands = map[string]command{
	"open_home":           {admin: "/", portal: "/"},
	"open_nodes":          {admin: "/nodes"},
	"open_model_library":  {admin: "/library"},
	"open_model_services": {admin: "/services"},
	"open_gateway":        {admin: "/gateway"},
	"open_audit":          {admin: "/audit"},
	"open_models":         {portal: "/models"},
	"open_keys":           {portal: "/keys"},
	"open_support":        {admin: "/support", portal: "/support"},
	listApplications:      {},
	listConversations:     {},
}

//go:embed locales/*/commands.json
var locales embed.FS

func loadAliases(source fs.FS) (map[string]command, error) {
	aliases := make(map[string]command)
	for _, path := range []string{"locales/en/commands.json", "locales/zh-cn/commands.json"} {
		body, err := fs.ReadFile(source, path)
		if err != nil {
			return nil, err
		}
		var local map[string][]string
		if err := json.Unmarshal(body, &local); err != nil {
			return nil, fmt.Errorf("decode %s: %w", path, err)
		}
		for id, definition := range commands {
			if len(local[id]) == 0 {
				return nil, fmt.Errorf("missing command %q in %s", id, path)
			}
			definition.id = id
			for _, phrase := range local[id] {
				normalized := normalize(phrase)
				if _, exists := aliases[normalized]; normalized == "" || exists {
					return nil, fmt.Errorf("invalid or duplicate alias for %q in %s", id, path)
				}
				aliases[normalized] = definition
			}
		}
		if len(local) != len(commands) {
			return nil, fmt.Errorf("unknown command in %s", path)
		}
	}
	return aliases, nil
}

func normalize(input string) string {
	if len(input) > MaxCommandBytes || !utf8.ValidString(input) {
		return ""
	}
	for _, r := range input {
		// Line breaks and format controls must never turn two lines into a command.
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			return ""
		}
	}
	return strings.Join(strings.Fields(strings.ToLower(input)), " ")
}

func (c command) destination(audience string) string {
	switch audience {
	case "admin":
		return c.admin
	case "portal":
		return c.portal
	default:
		return ""
	}
}
