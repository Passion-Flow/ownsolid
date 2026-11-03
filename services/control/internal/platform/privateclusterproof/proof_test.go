package privateclusterproof

import (
	"encoding/json"
	"strings"
	"testing"
)

func fixtureChallenge() Challenge {
	return Challenge{Version, "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333", strings.Repeat("a", 64), "arm64", 65532}
}
func TestChallengeAndObservedProofBinding(t *testing.T) {
	c := fixtureChallenge()
	pod := "44444444-4444-4444-8444-444444444444"
	raw, e := Encode(c)
	if e != nil {
		t.Fatal(e)
	}
	decoded, e := Decode(raw)
	if e != nil || decoded != c {
		t.Fatal(e)
	}
	p, e := makeProof(c, pod, "linux", "arm64", 65532)
	if e != nil {
		t.Fatal(e)
	}
	data, _ := json.Marshal(p)
	data = append(data, '\n')
	if out, e := Verify(data, c, pod); e != nil || out != p {
		t.Fatal(e)
	}
	for _, mutate := range []func(*Proof){func(p *Proof) { p.PodUID = c.NamespaceUID }, func(p *Proof) { p.ChallengeSHA256 = strings.Repeat("0", 64) }, func(p *Proof) { p.UID = 0 }, func(p *Proof) { p.Architecture = "amd64" }} {
		bad := p
		mutate(&bad)
		raw, _ := json.Marshal(bad)
		if _, e := Verify(append(raw, '\n'), c, pod); e == nil {
			t.Fatal("unbound proof accepted")
		}
	}
	for _, bad := range [][]byte{append([]byte(" "), raw...), append(raw, []byte(`,"nonce":"duplicate"`)...), []byte(`{"format_version":1,"format_version":1}`)} {
		if _, e := Decode(bad); e == nil {
			t.Fatal("noncanonical challenge accepted")
		}
	}
	if _, e := makeProof(c, pod, "darwin", "arm64", 65532); e == nil {
		t.Fatal("host fixture qualified Linux execution")
	}
	if _, e := makeProof(c, pod, "linux", "amd64", 65532); e == nil {
		t.Fatal("wrong architecture accepted")
	}
	if _, e := Verify(data[:len(data)-1], c, pod); e == nil {
		t.Fatal("truncated log accepted")
	}
}
