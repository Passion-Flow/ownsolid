// Package productagent resolves model-independent commands under the caller's session.
package productagent

import (
	"embed"
	json "encoding/json/v2"
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

type commandCatalog struct {
	aliases map[string]command
	locales map[string]map[string][]string
}

func loadCommands(source fs.FS) (commandCatalog, error) {
	catalog := commandCatalog{aliases: make(map[string]command), locales: make(map[string]map[string][]string)}
	for _, locale := range []struct{ tag, path string }{
		{"en", "locales/en/commands.json"},
		{"zh-CN", "locales/zh-cn/commands.json"},
	} {
		body, err := fs.ReadFile(source, locale.path)
		if err != nil {
			return commandCatalog{}, err
		}
		var local map[string][]string
		if err := json.Unmarshal(body, &local); err != nil {
			return commandCatalog{}, fmt.Errorf("decode %s: %w", locale.path, err)
		}
		for id, definition := range commands {
			if len(local[id]) == 0 {
				return commandCatalog{}, fmt.Errorf("missing command %q in %s", id, locale.path)
			}
			definition.id = id
			for _, phrase := range local[id] {
				normalized := normalize(phrase)
				if _, exists := catalog.aliases[normalized]; normalized == "" || exists {
					return commandCatalog{}, fmt.Errorf("invalid or duplicate alias for %q in %s", id, locale.path)
				}
				catalog.aliases[normalized] = definition
			}
		}
		if len(local) != len(commands) {
			return commandCatalog{}, fmt.Errorf("unknown command in %s", locale.path)
		}
		catalog.locales[locale.tag] = local
	}
	return catalog, nil
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
