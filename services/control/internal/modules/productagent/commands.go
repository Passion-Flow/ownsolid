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

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/applications"
)

// MaxCommandBytes bounds the command language at its input boundary.
const MaxCommandBytes = 128

type command struct {
	id              string
	risk            riskTier
	admin           string
	portal          string
	readTool        string
	nameArgument    bool
	applicationName string
}

const (
	listApplications  = "list_applications"
	listConversations = "list_conversations"
	findApplication   = "find_application"
)

var commands = map[string]command{
	"open_home":           {risk: riskNavigate, admin: "/", portal: "/"},
	"open_nodes":          {risk: riskNavigate, admin: "/nodes"},
	"open_model_library":  {risk: riskNavigate, admin: "/library"},
	"open_model_services": {risk: riskNavigate, admin: "/services"},
	"open_gateway":        {risk: riskNavigate, admin: "/gateway"},
	"open_audit":          {risk: riskNavigate, admin: "/audit"},
	"open_models":         {risk: riskNavigate, portal: "/models"},
	"open_keys":           {risk: riskNavigate, portal: "/keys"},
	"open_support":        {risk: riskNavigate, admin: "/support", portal: "/support"},
	listApplications:      {risk: riskRead},
	listConversations:     {risk: riskRead},
	findApplication:       {risk: riskRead, readTool: listApplications, nameArgument: true},
}

//go:embed locales/*/commands.json
var locales embed.FS

type commandCatalog struct {
	aliases      map[string]command
	namePrefixes map[string]command
	locales      map[string]map[string][]string
}

func loadCommands(source fs.FS) (commandCatalog, error) {
	catalog := commandCatalog{aliases: make(map[string]command), namePrefixes: make(map[string]command), locales: make(map[string]map[string][]string)}
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
				if definition.nameArgument {
					at := strings.IndexByte(phrase, '"')
					var example string
					if at < 1 || json.Unmarshal([]byte(phrase[at:]), &example) != nil {
						return commandCatalog{}, fmt.Errorf("invalid name template for %q", id)
					}
					if _, err := applications.NormalizeApplicationName(example); err != nil {
						return commandCatalog{}, err
					}
					prefix := normalize(phrase[:at])
					if _, exists := catalog.namePrefixes[prefix]; prefix == "" || exists {
						return commandCatalog{}, fmt.Errorf("invalid or duplicate name prefix for %q", id)
					}
					catalog.namePrefixes[prefix] = definition
					continue
				}
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
	for prefix := range catalog.namePrefixes {
		if _, exists := catalog.aliases[prefix]; exists {
			return commandCatalog{}, fmt.Errorf("name prefix conflicts with a fixed alias")
		}
	}
	return catalog, nil
}

// A Unicode scalar can use two six-byte JSON surrogate escapes. Bound the
// transport representation without narrowing the name contract's rune bound.
const maximumNameCommandBytes = MaxCommandBytes + 12*applications.MaximumApplicationNameCharacters + 2

func (s *Service) resolveCommand(input string) (command, error) {
	if len(input) > maximumNameCommandBytes || !utf8.ValidString(input) {
		return command{}, ErrInvalidCommand
	}
	if at := strings.IndexByte(input, '"'); at >= 0 {
		if cmd, exists := s.catalog.namePrefixes[normalize(input[:at])]; exists {
			var name string
			if err := json.Unmarshal([]byte(input[at:]), &name); err != nil {
				return command{}, ErrInvalidCommand
			}
			name, err := applications.NormalizeApplicationName(name)
			if err != nil {
				return command{}, ErrInvalidCommand
			}
			cmd.applicationName = name
			return cmd, nil
		}
	}
	normalized := normalize(input)
	if normalized == "" {
		return command{}, ErrInvalidCommand
	}
	if cmd, ok := s.catalog.aliases[normalized]; ok {
		return cmd, nil
	}
	return command{}, ErrUnknownCommand
}

func (c command) fingerprint(cursor string) string {
	if c.nameArgument {
		return executionDigest("application_name_v1", c.id, c.applicationName, cursor)
	}
	return commandFingerprint(c.id, cursor)
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
