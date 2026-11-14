package mysqlstore

import (
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"testing"
)

// Dummy sql.DB values below exercise immutable metadata predicates only, never
// connection/authentication/schema proof. Connector byte capture uses real files.
func TestMySQLInputBindingCapturesActualBytesDespiteFilesRestored(t *testing.T) {
	c := syntheticConfiguration(t)
	password, e := os.ReadFile(c.PasswordFile)
	if e != nil {
		t.Fatal(e)
	}
	ca, e := os.ReadFile(c.CAFile)
	if e != nil {
		t.Fatal(e)
	}
	configuration, original, e := connectorConfigurationAndInputs(c)
	if e != nil || configuration.Passwd != string(password) || original.passwordSHA256 != inputSHA256(password) || original.caSHA256 != inputSHA256(ca) {
		t.Fatal("actual connector inputs not captured", e)
	}
	for _, item := range []struct {
		path              string
		original, altered []byte
	}{{c.PasswordFile, password, []byte("different synthetic password")}, {c.CAFile, ca, append(append([]byte{}, ca...), '\n')}} {
		if e = os.WriteFile(item.path, item.altered, 0600); e != nil {
			t.Fatal(e)
		}
		_, changed, e := connectorConfigurationAndInputs(c)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(item.path, item.original, 0600); e != nil {
			t.Fatal(e)
		}
		s := &Store{pool: &sql.DB{}, configuration: c, inputs: changed}
		if s.InputMatches(c, inputSHA256(password), inputSHA256(ca)) {
			t.Fatal("restored files relabeled a connector built with different bytes")
		}
	}
	s := &Store{pool: &sql.DB{}, configuration: c, inputs: original}
	if !s.InputMatches(c, inputSHA256(password), inputSHA256(ca)) {
		t.Fatal("original connector metadata refused")
	}
	if fmt.Sprint(original) != "MYSQL_INPUT_BINDING_REDACTED" || fmt.Sprintf("%#v", original) != "MYSQL_INPUT_BINDING_REDACTED" {
		t.Fatal("binding diagnostics exposed private digest")
	}
}

func TestMySQLInputBindingComparesEveryTypedFieldAndRefusesMissing(t *testing.T) {
	c := syntheticConfiguration(t)
	_, inputs, e := connectorConfigurationAndInputs(c)
	if e != nil {
		t.Fatal(e)
	}
	s := &Store{pool: &sql.DB{}, configuration: c, inputs: inputs}
	for index := 0; index < reflect.TypeOf(c).NumField(); index++ {
		field := reflect.TypeOf(c).Field(index)
		if field.Type.Kind() == reflect.String || field.Type.Kind() == reflect.Int {
			bad := c
			v := reflect.ValueOf(&bad).Elem().Field(index)
			if v.Kind() == reflect.String {
				v.SetString(v.String() + "-changed")
			} else {
				v.SetInt(v.Int() + 1)
			}
			if s.InputMatches(bad, inputs.passwordSHA256, inputs.caSHA256) {
				t.Fatal("changed typed field accepted", field.Name)
			}
		} else if field.Name == "Policy" {
			for policyIndex := 0; policyIndex < field.Type.NumField(); policyIndex++ {
				bad := c
				v := reflect.ValueOf(&bad.Policy).Elem().Field(policyIndex)
				v.SetInt(v.Int() + 1)
				if s.InputMatches(bad, inputs.passwordSHA256, inputs.caSHA256) {
					t.Fatal("changed policy accepted", field.Type.Field(policyIndex).Name)
				}
			}
		} else {
			t.Fatal("uncovered typed configuration field", field.Name)
		}
	}
	for _, candidate := range []*Store{nil, {}, {pool: &sql.DB{}, configuration: c}} {
		if candidate.InputMatches(c, inputs.passwordSHA256, inputs.caSHA256) {
			t.Fatal("missing actual inputs accepted")
		}
	}
	for _, pair := range [][2]string{{"", inputs.caSHA256}, {inputs.passwordSHA256, ""}, {"foreign", inputs.caSHA256}, {inputs.passwordSHA256, "foreign"}} {
		if s.InputMatches(c, pair[0], pair[1]) {
			t.Fatal("foreign fingerprints accepted")
		}
	}
	if _, e = connectorConfiguration(c); e != nil {
		t.Fatal("existing connector API changed", e)
	}
}
