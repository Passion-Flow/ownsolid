package cellclusters

import (
	"bytes"
	"testing"
)

func TestSealerBindsTokensToClusterAndSecret(t *testing.T) {
	secret := bytes.Repeat([]byte{7}, 32)
	s, e := newSealer(secret)
	if e != nil {
		t.Fatal(e)
	}
	const cluster, other = "6f1c2a8e-4b3d-4c5e-9f7a-1b2c3d4e5f60", "0d9e8f7a-6b5c-4d3e-8f2a-1b0c9d8e7f6a"
	token := "K10" + string(bytes.Repeat([]byte{'a'}, 64)) + "::abcdef.0123456789abcdef"
	sealed, e := s.seal(cluster, token)
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(sealed, []byte(token)) || bytes.Contains(sealed, []byte("0123456789abcdef")) {
		t.Fatal("the token is readable in its sealed form")
	}
	if opened, e := s.open(cluster, sealed); e != nil || opened != token {
		t.Fatal(opened, e)
	}
	again, _ := s.seal(cluster, token)
	if bytes.Equal(again, sealed) {
		t.Fatal("two seals of one token are identical")
	}
	if _, e := s.open(other, sealed); e == nil {
		t.Fatal("a token opened for another cluster")
	}
	tampered := append([]byte(nil), sealed...)
	tampered[len(tampered)-1] ^= 1
	if _, e := s.open(cluster, tampered); e == nil {
		t.Fatal("a tampered token opened")
	}
	if _, e := s.open(cluster, sealed[:4]); e == nil {
		t.Fatal("a short value opened")
	}
	replaced, _ := newSealer(bytes.Repeat([]byte{8}, 32))
	if _, e := replaced.open(cluster, sealed); e == nil {
		t.Fatal("a token opened under another secret")
	}
	if _, e := newSealer(secret[:31]); e == nil {
		t.Fatal("a short secret was accepted")
	}
}
