package bootstrap

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/databaseaccess"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/mysqloperator"
)

func TestMySQLPacketFormatBindingAndProtectedPayload(t *testing.T) {
	r := request()
	canonical, e := json.Marshal(r)
	if e != nil {
		t.Fatal(e)
	}
	binding := digest([]byte("synthetic current MySQL and runtime proof"))
	p := MySQLPacket{mysqlPacketFormat, binding, Packet{formatVersion, packetPurpose, r, digest(canonical), base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{23}, passwordBytes))}}
	if e = p.validate(binding); e != nil {
		t.Fatal(e)
	}
	if fmt.Sprint(p) != "MYSQL_BOOTSTRAP_PACKET_REDACTED" || fmt.Sprintf("%#v", p) != "MYSQL_BOOTSTRAP_PACKET_REDACTED" {
		t.Fatal("packet formatting leaked credential")
	}
	for _, change := range []func(*MySQLPacket){func(v *MySQLPacket) { v.Format = "postgresql" }, func(v *MySQLPacket) { v.RuntimeBindingSHA256 = digest([]byte("another current target")) }, func(v *MySQLPacket) { v.RuntimeBindingSHA256 = strings.ToUpper(binding) }, func(v *MySQLPacket) { v.RuntimeBindingSHA256 = "" }, func(v *MySQLPacket) { v.Packet.RequestSHA256 = digest([]byte("changed request")) }, func(v *MySQLPacket) { v.Packet.Purpose = "other" }, func(v *MySQLPacket) { v.Packet.Password = "short" }} {
		bad := p
		change(&bad)
		if bad.validate(binding) == nil {
			t.Fatal("changed/foreign packet accepted")
		}
	}
	parent := t.TempDir()
	if e = os.Chmod(parent, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(parent, "packet.json")
	raw, e := json.Marshal(p)
	if e != nil {
		t.Fatal(e)
	}
	var retained MySQLPacket
	if e = mysqloperator.WriteDocument(path, raw, &retained); e != nil || retained.validate(binding) != nil {
		t.Fatal("durable packet readback", e)
	}
	if e = mysqloperator.WriteDocument(path, raw, &retained); e == nil {
		t.Fatal("overwrote private packet")
	}
	old, e := json.Marshal(p.Packet)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, old, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = mysqloperator.ReadDocument(path, mysqloperator.MaximumIntentBytes, &retained); e == nil {
		t.Fatal("PG packet accepted as MySQL target proof")
	}
}

func TestMySQLPacketMissingActualAuthorityHasZeroProjection(t *testing.T) {
	parent := t.TempDir()
	if e := os.Chmod(parent, 0700); e != nil {
		t.Fatal(e)
	}
	output := filepath.Join(parent, "absent.json")
	if e := PrepareMySQLPacket(t.Context(), nil, nil, "", output, databaseaccess.MySQLRuntimeInput{}); e == nil {
		t.Fatal("missing owner accepted")
	}
	if _, e := os.Lstat(output); !os.IsNotExist(e) {
		t.Fatal("unverified owner created credential")
	}
	for _, command := range []string{ApplyPacketCommand, CheckPacketCommand, "invalid"} {
		if status, e := RunMySQLPacket(t.Context(), nil, nil, command, output, databaseaccess.MySQLRuntimeInput{}); e == nil || status != "" {
			t.Fatal("missing selected proof projected success")
		}
	}
}
