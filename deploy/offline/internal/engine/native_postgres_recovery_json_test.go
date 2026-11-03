package engine

import (
	"bytes"
	"encoding/json"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"reflect"
	"testing"
)

func TestNativeRecoveryProfileRawFifthStoreMustBeRejected(t *testing.T) {
	task := nativeRecoveryFixture(NativePostgreSQLRestore)
	raw, e := json.Marshal(task.NativePostgreSQLRecovery)
	if e != nil {
		t.Fatal(e)
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		t.Fatal("fixture object")
	}
	var stores []json.RawMessage
	if json.Unmarshal(object["stores"], &stores) != nil || len(stores) != 4 {
		t.Fatal("fixture original fourstores")
	}
	stores = append(stores, json.RawMessage(`{"owner":{"scope_id":"foreign","operation_id":"unowned","role":"unexpected-fifth"},"target":"/host/foreign"}`))
	object["stores"], _ = json.Marshal(stores)
	raw, _ = json.Marshal(object)
	var decoded NativePostgreSQLRecoveryProfile
	if e := json.Unmarshal(raw, &decoded); e == nil {
		task.NativePostgreSQLRecovery = &decoded
		if task.valid() {
			t.Fatal("fifth raw store silently truncated; invalid protected input accepted")
		}
		t.Fatal("fifth raw store not refused at decode")
	}
}

func TestNativeRecoveryProfileJSONRetainsCanonicalNativeAndLegacyBytes(t *testing.T) {
	for _, kind := range []string{NativePostgreSQLBackup, NativePostgreSQLRestore, NativePostgreSQLRestoreVerify} {
		t.Run(kind, func(t *testing.T) {
			task := nativeRecoveryFixture(kind)
			before, e := json.Marshal(task.NativePostgreSQLRecovery)
			if e != nil {
				t.Fatal(e)
			}
			var got NativePostgreSQLRecoveryProfile
			if e = json.Unmarshal(before, &got); e != nil {
				t.Fatal("original profile refused", e)
			}
			task.NativePostgreSQLRecovery = &got
			if !task.valid() {
				t.Fatal("decoded original task changed")
			}
			after, e := json.Marshal(got)
			if e != nil || !bytes.Equal(before, after) {
				t.Fatal("original native profile bytes changed")
			}
		})
	}
	var legacy DatabaseTask
	raw, e := json.Marshal(legacy)
	if e != nil || bytes.Contains(raw, []byte("native_postgresql_recovery")) {
		t.Fatal("legacy omitted native field changed")
	}
}

func TestNativeRecoveryProfileJSONRejectsMalformedAndContradictoryRawShape(t *testing.T) {
	task := nativeRecoveryFixture(NativePostgreSQLRestore)
	original, _ := json.Marshal(task.NativePostgreSQLRecovery)
	object := func() map[string]json.RawMessage {
		var v map[string]json.RawMessage
		json.Unmarshal(original, &v)
		return v
	}
	cases := map[string][]byte{"null": []byte("null"), "array": []byte("[]"), "empty object": []byte("{}"), "malformed": []byte("{")}
	for name, count := range map[string]int{"zero stores": 0, "three stores": 3, "five valid stores": 5} {
		v := object()
		var stores []json.RawMessage
		json.Unmarshal(v["stores"], &stores)
		if count <= 4 {
			stores = stores[:count]
		} else {
			stores = append(stores, stores[0])
		}
		v["stores"], _ = json.Marshal(stores)
		cases[name], _ = json.Marshal(v)
	}
	v := object()
	delete(v, "stores")
	cases["missing stores"], _ = json.Marshal(v)
	v = object()
	v["stores"] = json.RawMessage("null")
	cases["null stores"], _ = json.Marshal(v)
	v = object()
	v["stores"] = json.RawMessage(`{"owner":"not-array"}`)
	cases["stores object"], _ = json.Marshal(v)
	v = object()
	v["Stores"] = v["stores"]
	delete(v, "stores")
	cases["top case alias"], _ = json.Marshal(v)
	v = object()
	v["unexpected_setting"] = json.RawMessage("false")
	cases["unknown field"], _ = json.Marshal(v)
	v = object()
	var stores []json.RawMessage
	json.Unmarshal(v["stores"], &stores)
	var store map[string]json.RawMessage
	json.Unmarshal(stores[0], &store)
	store["Owner"] = store["owner"]
	delete(store, "owner")
	stores[0], _ = json.Marshal(store)
	v["stores"], _ = json.Marshal(stores)
	cases["nested case alias"], _ = json.Marshal(v)
	cases["duplicate root field"] = append(append([]byte(`{"stores":`), object()["stores"]...), append([]byte(","), original[1:]...)...)
	cases["duplicate nested field"] = bytes.Replace(original, []byte(`"role":`), []byte(`"role":"unexpected","role":`), 1)
	cases["invalid utf8"] = bytes.Replace(original, []byte("/fixture"), []byte{255}, 1)
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			current := nativeRecoveryFixture(NativePostgreSQLRestore).NativePostgreSQLRecovery
			before, _ := json.Marshal(current)
			if e := json.Unmarshal(raw, current); e == nil {
				t.Fatal("invalid protected native shape accepted")
			}
			after, _ := json.Marshal(current)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("failed decode partially changed original profile")
			}
		})
	}
	var missing *NativePostgreSQLRecoveryProfile
	if e := missing.UnmarshalJSON(original); e == nil {
		t.Fatal("nil receiver accepted")
	}
}

func TestNativeRecoveryProfileJSONRetainsOptionalRegistryAndPinnedRootShape(t *testing.T) {
	task := nativeRecoveryFixture(NativePostgreSQLRestoreVerify)
	p := task.NativePostgreSQLRecovery
	owner := func(role string) Ownership {
		return Ownership{task.Owner.ScopeID, task.Owner.OperationID, role + "-" + task.Owner.OperationID}
	}
	p.Scaffold = &NativePostgreSQLVerifierPlaceholder{Owner: owner(NativePostgreSQLScaffoldRole), Target: "/fixture"}
	p.Registry = &NativePostgreSQLRegistryMount{Owner: Ownership{task.Owner.ScopeID, task.Owner.OperationID, NativePostgreSQLRegistryRole}, Target: "/fixture/registry"}
	p.PreparedRelease = &NativePostgreSQLPreparedMount{Owner: owner(NativePostgreSQLPreparedRole), Target: "/fixture/prepared"}
	p.BackupReference.RegistryConfigured = true
	p.BackupReference.Registry = &wire.NativePostgreSQLRegistryReference{ReceiptSHA256: nativeObservationHash("1"), ManifestSHA256: nativeObservationHash("2"), PayloadTreeSHA256: nativeObservationHash("3"), Pins: 1, Files: 1, Bytes: 1}
	for i, store := range p.Stores {
		p.RestoreRequest.TargetRoots = append(p.RestoreRequest.TargetRoots, wire.PostgreSQLRestoreRoot{Path: store.Target, Device: 1, Inode: uint64(i + 10), UID: uint32(task.UID), GID: uint32(task.GID)})
	}
	if !task.valid() {
		t.Fatal("original bound registry/root fixture invalid")
	}
	before, _ := json.Marshal(p)
	var decoded NativePostgreSQLRecoveryProfile
	if e := json.Unmarshal(before, &decoded); e != nil {
		t.Fatal("original optional registry/pinnedroots refused", e)
	}
	task.NativePostgreSQLRecovery = &decoded
	after, _ := json.Marshal(decoded)
	if !task.valid() || !bytes.Equal(before, after) {
		t.Fatal("original optional registry/pinnedroots changed")
	}
}
