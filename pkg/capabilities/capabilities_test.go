package capabilities

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func document(option string) string {
	return `{"format_version":1,"registries":[{"id":"sizes","name":"Sizes","description":"How big.","options":[` + option + `]}]}`
}

func TestParseRefuses(t *testing.T) {
	cases := map[string]string{
		"empty":             ``,
		"wrong format":      `{"format_version":2,"registries":[]}`,
		"no registries":     `{"format_version":1,"registries":[]}`,
		"unknown key":       `{"format_version":1,"registries":[],"extra":1}`,
		"trailing content":  `{"format_version":1,"registries":[]} {}`,
		"no options":        `{"format_version":1,"registries":[{"id":"a","name":"A","description":"B","options":[]}]}`,
		"registry ID":       `{"format_version":1,"registries":[{"id":"A b","name":"A","description":"B","options":[{"id":"a","name":"A","state":"ready","roadmap_item":"ABC-001"}]}]}`,
		"no description":    `{"format_version":1,"registries":[{"id":"a","name":"A","options":[{"id":"a","name":"A","state":"ready","roadmap_item":"ABC-001"}]}]}`,
		"twice":             `{"format_version":1,"registries":[{"id":"a","name":"A","description":"B","options":[{"id":"a","name":"A","state":"ready","roadmap_item":"ABC-001"}]},{"id":"a","name":"A","description":"B","options":[{"id":"a","name":"A","state":"ready","roadmap_item":"ABC-001"}]}]}`,
		"option ID":         document(`{"id":"A","name":"A","state":"ready","roadmap_item":"ABC-001"}`),
		"option twice":      document(`{"id":"a","name":"A","state":"ready","roadmap_item":"ABC-001"},{"id":"a","name":"B","state":"ready","roadmap_item":"ABC-001"}`),
		"no name":           document(`{"id":"a","state":"ready","roadmap_item":"ABC-001"}`),
		"unknown state":     document(`{"id":"a","name":"A","state":"soon","reason":"R","roadmap_item":"ABC-001"}`),
		"bad item":          document(`{"id":"a","name":"A","state":"planned","reason":"R","roadmap_item":"later"}`),
		"ready, no item":    document(`{"id":"a","name":"A","state":"ready"}`),
		"ready, reason":     document(`{"id":"a","name":"A","state":"ready","roadmap_item":"ABC-001","reason":"R"}`),
		"planned, no item":  document(`{"id":"a","name":"A","state":"planned","reason":"R"}`),
		"planned, no why":   document(`{"id":"a","name":"A","state":"planned","roadmap_item":"ABC-001"}`),
		"licensed, no why":  document(`{"id":"a","name":"A","state":"licensed","roadmap_item":"ABC-001"}`),
		"unsupported, none": document(`{"id":"a","name":"A","state":"unsupported"}`),
		"unsupported, item": document(`{"id":"a","name":"A","state":"unsupported","reason":"R","roadmap_item":"ABC-001"}`),
	}
	for name, raw := range cases {
		if _, e := parse([]byte(raw)); e == nil {
			t.Errorf("%s: parsed", name)
		}
	}
}

func TestParseAcceptsEveryState(t *testing.T) {
	registries, e := parse([]byte(document(`{"id":"a","name":"A","state":"ready","roadmap_item":"ABC-001","note":"N"},` +
		`{"id":"b","name":"B","state":"planned","roadmap_item":"ABC-002","reason":"R"},` +
		`{"id":"c","name":"C","state":"licensed","roadmap_item":"ABC-003","reason":"R"},` +
		`{"id":"d","name":"D","state":"unsupported","reason":"R"}`)))
	if e != nil || len(registries) != 1 || len(registries[0].Options) != 4 {
		t.Fatal(registries, e)
	}
}

func TestLookupFindsRegistriesAndOptions(t *testing.T) {
	sizes, found := Lookup(InstallSizes)
	if !found {
		t.Fatal("install sizes are not declared")
	}
	if option, found := sizes.Lookup("single-node"); !found || option.State != Ready {
		t.Fatal(option, found)
	}
	if _, found := sizes.Lookup("huge"); found {
		t.Fatal("found an undeclared size")
	}
	if _, found := Lookup("nothing"); found {
		t.Fatal("found an undeclared registry")
	}
}

func TestRegistriesReturnsCopies(t *testing.T) {
	first := Registries()
	first[0].Options[0].State = Unsupported
	first[0].Name = "changed"
	second := Registries()
	if second[0].Options[0].State == Unsupported || second[0].Name == "changed" {
		t.Fatal("a caller changed the declaration")
	}
}

var roadmapItem = regexp.MustCompile(`(?m)^- \[( |x)\] ([A-Z]+-[0-9]+) `)

// roadmap reads which items exist and which are ticked, from the repository's roadmap.
func roadmap(t *testing.T) map[string]bool {
	t.Helper()
	files, e := filepath.Glob(filepath.Join("..", "..", "docs", "product", "roadmap", "*.md"))
	if e != nil || len(files) == 0 {
		t.Fatal("no roadmap", e)
	}
	ticked := map[string]bool{}
	for _, file := range files {
		raw, e := os.ReadFile(file)
		if e != nil {
			t.Fatal(e)
		}
		for _, match := range roadmapItem.FindAllStringSubmatch(string(raw), -1) {
			ticked[match[2]] = match[1] == "x"
		}
	}
	return ticked
}

// A row moves with its item: ADR 0036 makes "ready" a claim that needs the tick first.
func TestEveryRowAgreesWithTheRoadmap(t *testing.T) {
	ticked := roadmap(t)
	for _, registry := range Registries() {
		for _, option := range registry.Options {
			name := registry.ID + "/" + option.ID
			if option.State == Unsupported {
				continue
			}
			done, exists := ticked[option.RoadmapItem]
			switch {
			case !exists:
				t.Errorf("%s names %s, which is not on the roadmap", name, option.RoadmapItem)
			case option.State == Planned && done:
				t.Errorf("%s is planned but %s is ticked: move the row in pkg/capabilities/declaration.json to ready", name, option.RoadmapItem)
			case option.State != Planned && !done:
				t.Errorf("%s is %s but %s is open: tick the item first", name, option.State, option.RoadmapItem)
			}
		}
	}
}

func TestTextIsPlainAndEnglish(t *testing.T) {
	for _, registry := range Registries() {
		for _, option := range registry.Options {
			for _, text := range []string{option.Name, option.Reason, option.Note} {
				if strings.TrimSpace(text) != text {
					t.Errorf("%s/%s: stray space in %q", registry.ID, option.ID, text)
				}
				for _, r := range text {
					if r > 127 {
						t.Errorf("%s/%s: %q is not English; translations live in locales", registry.ID, option.ID, text)
					}
				}
			}
		}
	}
}
