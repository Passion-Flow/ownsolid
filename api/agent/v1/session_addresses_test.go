package v1

import (
	"fmt"
	"slices"
	"testing"
)

func TestValidateSessionAddresses(t *testing.T) {
	valid := [][]string{nil, {"10.0.0.1:8443"}, {"gw-1.cell.example:443", "[2001:db8::1]:9443"}}
	for _, v := range valid {
		if e := ValidateSessionAddresses(v); e != nil {
			t.Fatalf("%v: %v", v, e)
		}
	}
	many := make([]string, MaxSessionAddresses+1)
	for i := range many {
		many[i] = fmt.Sprintf("10.0.0.%d:443", i+1)
	}
	invalid := [][]string{
		{"10.0.0.1"}, {":443"}, {"10.0.0.1:0"}, {"10.0.0.1:65536"}, {"10.0.0.1:080"}, {"10.0.0.1:https"},
		{"a b:443"}, {"user@host:443"}, {"host/path:443"}, {"10.0.0.1:443", "10.0.0.1:443"}, many,
	}
	for _, v := range invalid {
		if ValidateSessionAddresses(v) == nil {
			t.Fatalf("%v accepted", v)
		}
	}
}

func TestSessionAddressOrderIsStableAndComplete(t *testing.T) {
	addresses := []string{"10.0.0.1:443", "10.0.0.2:443", "10.0.0.3:443"}
	input := slices.Clone(addresses)
	first := SessionAddressOrder("7f9c2ba4-e88f-4a5b-9b2e-3a1d2c4e5f60", addresses)
	if !slices.Equal(input, addresses) {
		t.Fatal("input changed")
	}
	again := SessionAddressOrder("7f9c2ba4-e88f-4a5b-9b2e-3a1d2c4e5f60", []string{"10.0.0.3:443", "10.0.0.1:443", "10.0.0.2:443"})
	if !slices.Equal(first, again) {
		t.Fatalf("order depends on input order: %v %v", first, again)
	}
	sorted := slices.Clone(first)
	slices.Sort(sorted)
	if !slices.Equal(sorted, addresses) {
		t.Fatalf("order lost or repeated an address: %v", first)
	}
	if len(SessionAddressOrder("node", nil)) != 0 {
		t.Fatal("empty list ordered to something")
	}
}

// Rendezvous ordering must spread first choices evenly and, when a gateway is removed, move
// only the nodes whose first choice it was.
func TestSessionAddressOrderSpreadsAndMovesLittle(t *testing.T) {
	addresses := []string{"10.0.0.1:443", "10.0.0.2:443", "10.0.0.3:443", "10.0.0.4:443"}
	const nodes = 20000
	counts := map[string]int{}
	moved := 0
	for i := range nodes {
		node := fmt.Sprintf("00000000-0000-4000-8000-%012d", i)
		order := SessionAddressOrder(node, addresses)
		counts[order[0]]++
		after := SessionAddressOrder(node, addresses[1:])
		if order[0] != addresses[0] && after[0] != order[0] {
			moved++
		}
		if order[0] == addresses[0] && after[0] != order[1] {
			t.Fatalf("node %s did not fail over to its second choice", node)
		}
	}
	for a, n := range counts {
		if share := float64(n) / nodes; share < 0.23 || share > 0.27 {
			t.Fatalf("%s holds %.3f of nodes", a, share)
		}
	}
	if moved != 0 {
		t.Fatalf("%d nodes moved off a gateway that stayed", moved)
	}
}
