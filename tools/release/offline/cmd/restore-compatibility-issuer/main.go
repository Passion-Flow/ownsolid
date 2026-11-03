package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	compat "github.com/Passion-Flow/ownsolid/api/release/restorecompat"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func run(args []string, output io.Writer) error {
	bad := func() error { return errors.New("RELEASE_RESTORE_COMPATIBILITY_ISSUER_INVALID") }
	f := flag.NewFlagSet("restore-compatibility-issuer", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	binding := f.String("binding-file", "", "Protected reviewed exact compatibility binding")
	keyFile := f.String("private-key-file", "", "Protected release signing-key reference")
	name := f.String("output-file", "", "Exclusive output in private canonical directory")
	if f.Parse(args) != nil || f.NArg() != 0 || *binding == "" || *keyFile == "" || !filepath.IsAbs(*name) || filepath.Clean(*name) != *name {
		return bad()
	}
	raw, e := bundle.ReadProtectedConfiguration(*binding)
	if e != nil {
		return bad()
	}
	var b compat.RestoreCompatibilityBinding
	if wire.StrictJSON(raw, &b) != nil || b.Validate() != nil {
		return bad()
	}
	raw, e = bundle.ReadProtectedConfiguration(*keyFile)
	if e != nil {
		return bad()
	}
	encoded := strings.TrimSpace(string(raw))
	key, e := base64.StdEncoding.Strict().DecodeString(encoded)
	if e != nil || base64.StdEncoding.EncodeToString(key) != encoded || len(key) != ed25519.PrivateKeySize || !bytes.Equal(ed25519.NewKeyFromSeed(ed25519.PrivateKey(key).Seed()), key) {
		return bad()
	}
	a, e := compat.SignRestoreCompatibility(b, ed25519.PrivateKey(key))
	if e != nil {
		return bad()
	}
	for i := range key {
		key[i] = 0
	}
	body, e := json.Marshal(a)
	if e != nil {
		return bad()
	}
	body = append(body, '\n')
	parent := filepath.Dir(*name)
	resolved, e := filepath.EvalSymlinks(parent)
	if e != nil || resolved != parent {
		return bad()
	}
	before, e := os.Stat(parent)
	if e != nil || !before.IsDir() || before.Mode().Perm() != 0700 {
		return bad()
	}
	owner, ok := before.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Geteuid() {
		return bad()
	}
	r, e := os.OpenRoot(parent)
	if e != nil {
		return bad()
	}
	defer r.Close()
	file, e := r.OpenFile(filepath.Base(*name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return bad()
	}
	created, e := file.Stat()
	if e != nil {
		file.Close()
		return bad()
	}
	n, we := file.Write(body)
	se := file.Sync()
	ce := file.Close()
	if n != len(body) || we != nil || se != nil || ce != nil {
		return bad()
	}
	dir, e := os.Open(parent)
	if e != nil {
		return bad()
	}
	se = dir.Sync()
	ce = dir.Close()
	after, e := os.Stat(parent)
	if e != nil || !os.SameFile(before, after) || se != nil || ce != nil {
		return bad()
	}
	readback, e := bundle.ReadProtectedConfiguration(*name)
	current, ce := r.Lstat(filepath.Base(*name))
	if e != nil || ce != nil || !os.SameFile(created, current) || !bytes.Equal(readback, body) {
		return bad()
	}
	return json.NewEncoder(output).Encode(map[string]string{"status": "issued_exact_signed_restore_compatibility_only", "certificate_sha256": wire.Digest(body), "key_id": a.KeyID, "qualification": "signature_only_not_compatibility_review_restoration_or_target_acceptance"})
}

func main() {
	if e := run(os.Args[1:], os.Stdout); e != nil {
		fmt.Fprintln(os.Stderr, e.Error())
		os.Exit(1)
	}
}
