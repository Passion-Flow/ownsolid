package config

import (
	"reflect"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestDatabaseOptionalFieldsKeepStrictConcreteTypes(t *testing.T) {
	for name, source := range map[string]string{
		"runtime_account": "database:\n  mysql_runtime_access:\n    account_host: localhost\n    intent_file: /private/synthetic/intent.json\n",
		"native_backup":   "database:\n  mysql_backup:\n    operation_seconds: 120\n    client_file: /private/synthetic/mysql\n",
		"lock_timeout":    "database:\n  lock_timeout_milliseconds: 1500\n",
	} {
		t.Run(name, func(t *testing.T) {
			if e := validateDatabaseYAML([]byte(source)); e != nil {
				t.Fatal("typed optional field refused", e)
			}
		})
	}
	for name, source := range map[string]string{
		"null_account":    "database:\n  mysql_runtime_access: null\n",
		"null_backup":     "database:\n  mysql_backup: null\n",
		"null_lock":       "database:\n  lock_timeout_milliseconds: null\n",
		"quoted_integer":  "database:\n  lock_timeout_milliseconds: '1500'\n",
		"false_integer":   "database:\n  lock_timeout_milliseconds: false\n",
		"scalar_account":  "database:\n  mysql_runtime_access: false\n",
		"unknown_nested":  "database:\n  mysql_runtime_access:\n    foreign: false\n",
		"duplicate_field": "database:\n  mysql_runtime_access:\n    account_host: localhost\n    account_host: foreign.invalid\n",
		"boolean_seconds": "database:\n  mysql_backup:\n    operation_seconds: false\n",
		"alias_mapping":   "anchor: &policy\n  account_host: localhost\ndatabase:\n  mysql_runtime_access: *policy\n",
	} {
		t.Run(name, func(t *testing.T) {
			if e := validateDatabaseYAML([]byte(source)); e == nil {
				t.Fatal("ambiguous/foreign/wrong typed optional field accepted")
			}
		})
	}
}

func TestOptionalBooleanFalseKeepsTypedMeaning(t *testing.T) {
	// This helper-level syntax fixture explicitly models an optional boolean;
	// it is not a new deployment setting or a configurable security bypass.
	type nested struct {
		Enabled *bool `yaml:"enabled"`
	}
	type optional struct {
		Nested *nested `yaml:"nested"`
	}
	for _, enabled := range []bool{false, true} {
		source := "nested:\n  enabled: false\n"
		if enabled {
			source = "nested:\n  enabled: true\n"
		}
		var node yaml.Node
		if e := yaml.Unmarshal([]byte(source), &node); e != nil {
			t.Fatal(e)
		}
		if e := strictKnowledgeYAML(node.Content[0], reflect.TypeOf(optional{})); e != nil {
			t.Fatal(e)
		}
		var actual optional
		if e := node.Content[0].Decode(&actual); e != nil || actual.Nested == nil || actual.Nested.Enabled == nil || *actual.Nested.Enabled != enabled {
			t.Fatal("typed boolean meaning changed", e)
		}
	}
	for _, value := range []string{"'false'", "null", "0", "[]", "{}"} {
		var node yaml.Node
		if e := yaml.Unmarshal([]byte("nested:\n  enabled: "+value+"\n"), &node); e != nil {
			t.Fatal(e)
		}
		if e := strictKnowledgeYAML(node.Content[0], reflect.TypeOf(optional{})); e == nil {
			t.Fatal("wrong optional boolean shape accepted")
		}
	}
}
