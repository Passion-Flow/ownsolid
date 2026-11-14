package dameng

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestLongValuePolicyIsVersionedAndBounded(t *testing.T) {
	c := connectorConfigurationFixture()
	if c.Validate() != nil || c.Policy.native().Capacity().LongValues {
		t.Fatal("legacy profile changed")
	}
	c.FormatVersion = 2
	c.Policy.MaximumLongValueBytes = 2 << 20
	c.Policy.TransferChunkBytes = 4096
	c.Policy.MaximumRetainedBytes = 16 << 20
	if c.Validate() != nil || c.Policy.native().FormatVersion != 3 || !c.Policy.native().Capacity().LongValues {
		t.Fatal("explicit long value profile rejected")
	}
	for _, change := range []func(*ConnectionConfiguration){
		func(v *ConnectionConfiguration) { v.FormatVersion = 1 },
		func(v *ConnectionConfiguration) { v.Policy.MaximumLongValueBytes = 0 },
		func(v *ConnectionConfiguration) { v.Policy.MaximumLongValueBytes = 65 << 20 },
		func(v *ConnectionConfiguration) { v.Policy.TransferChunkBytes = 4097 },
		func(v *ConnectionConfiguration) { v.Policy.TransferChunkBytes = 0 },
		func(v *ConnectionConfiguration) { v.Policy.MaximumRetainedBytes = 8 << 20 },
		func(v *ConnectionConfiguration) { v.Policy.MaximumRetainedBytes = 513 << 20 },
	} {
		invalid := c
		change(&invalid)
		if invalid.Validate() == nil {
			t.Fatal("contradictory capacity accepted")
		}
	}
	raw, e := json.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	var copied ConnectionConfiguration
	if json.Unmarshal(raw, &copied) != nil || !reflect.DeepEqual(c, copied) {
		t.Fatal("versioned fields lost")
	}
	capacity := c.Policy.native().Capacity()
	if capacity.LongValueBytes != 2<<20 || capacity.RetainedBytes != 16<<20 || capacity.InlineResultBytes != c.Policy.MaximumResultBytes {
		t.Fatal("capability projection mismatch")
	}
}

func TestLongValueProtectedLoadersRejectMissingNullAndFalse(t *testing.T) {
	c := connectorConfigurationFixture()
	c.FormatVersion = 2
	c.Policy.MaximumLongValueBytes = 2 << 20
	c.Policy.TransferChunkBytes = 4096
	c.Policy.MaximumRetainedBytes = 16 << 20
	manager := c.Policy.native()
	manager.ConnectionFile = "/private/connector/connection"
	for _, entry := range []struct {
		name  string
		value any
		load  func(string) error
	}{
		{"connector", c, func(path string) error { _, e := LoadConnectionConfiguration(path, ""); return e }},
		{"manager", manager, func(path string) error { _, e := LoadODBCConfig(path); return e }},
	} {
		t.Run(entry.name, func(t *testing.T) {
			raw, e := json.Marshal(entry.value)
			if e != nil {
				t.Fatal(e)
			}
			if e := entry.load(odbcPrivatePolicyFixture(t, raw)); e != nil {
				t.Fatal("valid protected long configuration rejected", e)
			}
			for _, field := range []string{"maximum_long_value_bytes", "transfer_chunk_bytes", "maximum_retained_bytes"} {
				var doc map[string]any
				if e := json.Unmarshal(raw, &doc); e != nil {
					t.Fatal(e)
				}
				target := doc
				if entry.name == "connector" {
					target = doc["policy"].(map[string]any)
				}
				original := target[field]
				for _, bad := range []any{nil, false, "4096", float64(0)} {
					target[field] = bad
					encoded, _ := json.Marshal(doc)
					if entry.load(odbcPrivatePolicyFixture(t, encoded)) == nil {
						t.Fatal("invalid field accepted", field)
					}
				}
				delete(target, field)
				encoded, _ := json.Marshal(doc)
				if entry.load(odbcPrivatePolicyFixture(t, encoded)) == nil {
					t.Fatal("omitted field accepted", field)
				}
				target[field] = original
			}
			// Duplicate keys must remain refused even if their values agree.
			duplicate := strings.Replace(string(raw), `"transfer_chunk_bytes":4096`, `"transfer_chunk_bytes":4096,"transfer_chunk_bytes":4096`, 1)
			if entry.load(odbcPrivatePolicyFixture(t, []byte(duplicate))) == nil {
				t.Fatal("duplicate long field accepted")
			}
		})
	}
}
