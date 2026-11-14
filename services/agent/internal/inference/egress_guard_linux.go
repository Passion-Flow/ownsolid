//go:build linux

package inference

import (
	"bytes"
	"net"
	"slices"
	"strings"
	"sync"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"github.com/google/nftables/userdata"
	"golang.org/x/sys/unix"
)

const (
	// egressTable is the nftables table the agent owns. Nothing else may add to it: the
	// guard rebuilds the whole table when it finds a rule or chain it did not write.
	egressTable = "ownsolid_workload_egress"
	// egressPriority puts the guard ahead of Docker's filter chains, which hook at
	// priority 0 through either iptables backend, so nothing Docker accepts gets past it.
	// A packet dropped by any base chain is dropped, whichever table holds the chain.
	egressPriority nftables.ChainPriority = -10
	// egressComment prefixes the comment on every guard rule; `nft list ruleset` shows it.
	egressComment = "ownsolid workload egress "
)

// guardChain is one base chain of the guard table and the rules it holds per bridge.
type guardChain struct {
	name  string
	hook  *nftables.ChainHook
	rules func(bridge string) [][]expr.Any
}

// guardChains are the guard's base chains. Forwarded traffic covers every destination off
// the host and the embedded resolver's forwarded queries; input covers host services.
var guardChains = []guardChain{
	{name: "forward", hook: nftables.ChainHookForward, rules: forwardRules},
	{name: "input", hook: nftables.ChainHookInput, rules: inputRules},
}

// forwardRules refuse everything routed out of the bridge, at once and with "administratively
// prohibited" so a sender fails fast instead of waiting for a timeout, and drop anything
// routed into it: the published port is bound to loopback, so no legitimate packet for the
// container is ever forwarded.
func forwardRules(bridge string) [][]expr.Any {
	return [][]expr.Any{
		append(matchInterface(expr.MetaKeyIIFNAME, bridge), rejectProhibited()),
		append(matchInterface(expr.MetaKeyOIFNAME, bridge), &expr.Verdict{Kind: expr.VerdictDrop}),
	}
}

// inputRules let the container answer connections the host opened, which is how the
// loopback port publication reaches it, and refuse every connection it opens to the host.
func inputRules(bridge string) [][]expr.Any {
	reply := append(matchInterface(expr.MetaKeyIIFNAME, bridge),
		&expr.Ct{Register: 1, Key: expr.CtKeySTATE},
		&expr.Bitwise{SourceRegister: 1, DestRegister: 1, Len: 4,
			Mask: binaryutil.NativeEndian.PutUint32(expr.CtStateBitESTABLISHED | expr.CtStateBitRELATED),
			Xor:  binaryutil.NativeEndian.PutUint32(0)},
		&expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: binaryutil.NativeEndian.PutUint32(0)},
		&expr.Verdict{Kind: expr.VerdictAccept})
	return [][]expr.Any{reply, append(matchInterface(expr.MetaKeyIIFNAME, bridge), rejectProhibited())}
}

func matchInterface(key expr.MetaKey, name string) []expr.Any {
	padded := make([]byte, unix.IFNAMSIZ)
	copy(padded, name)
	return []expr.Any{&expr.Meta{Key: key, Register: 1}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: padded}}
}

func rejectProhibited() expr.Any {
	return &expr.Reject{Type: unix.NFT_REJECT_ICMPX_UNREACH, Code: unix.NFT_REJECT_ICMPX_ADMIN_PROHIBITED}
}

// nftablesGuard keeps the guard table in the agent's network namespace, which must be the
// host's. Every change replaces the whole table in one netlink batch, so the kernel
// applies it atomically and no guarded bridge is ever unprotected during a change.
type nftablesGuard struct {
	mu sync.Mutex
}

func newEgressGuard() egressGuard { return &nftablesGuard{} }

// Ensure implements egressGuard.
func (g *nftablesGuard) Ensure(bridge string) error {
	if !bridgeName.MatchString(bridge) {
		return fail("INFERENCE_CONFIGURATION_INVALID")
	}
	// A missing bridge means the agent does not share the host's network namespace, so
	// rules written here would protect nothing.
	if _, e := net.InterfaceByName(bridge); e != nil {
		return fail(egressGuardUnavailable)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	conn, e := nftables.New()
	if e != nil {
		return fail(egressGuardUnavailable)
	}
	current, e := readGuard(conn)
	if e != nil {
		return fail(egressGuardUnavailable)
	}
	if current.protects(bridge) {
		return nil
	}
	if e = writeGuard(conn, current.present, append(current.liveBridges(), bridge)); e != nil {
		return fail(egressGuardUnavailable)
	}
	if confirmed, e := readGuard(conn); e != nil || !confirmed.protects(bridge) {
		return fail(egressGuardUnavailable)
	}
	return nil
}

// Remove implements egressGuard.
func (g *nftablesGuard) Remove(bridge string) error {
	if !bridgeName.MatchString(bridge) {
		return fail("INFERENCE_CONFIGURATION_INVALID")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	conn, e := nftables.New()
	if e != nil {
		return fail(egressGuardUnavailable)
	}
	current, e := readGuard(conn)
	if e != nil {
		return fail(egressGuardUnavailable)
	}
	if !current.present || current.sound && !slices.Contains(current.bridges, bridge) {
		return nil
	}
	remaining := slices.DeleteFunc(current.liveBridges(), func(name string) bool { return name == bridge })
	if e = writeGuard(conn, true, remaining); e != nil {
		return fail(egressGuardUnavailable)
	}
	return nil
}

// guardState is the guard table as the kernel holds it.
type guardState struct {
	// present reports whether the table exists.
	present bool
	// sound reports whether the table holds exactly the guard chains and only complete,
	// unaltered rule groups written by the guard.
	sound bool
	// bridges lists every bridge named by a guard rule comment, in table order.
	bridges []string
}

func (s guardState) protects(bridge string) bool {
	return s.sound && slices.Contains(s.bridges, bridge)
}

// liveBridges returns the guarded bridges whose interface still exists. A bridge whose
// network is gone needs no rules, so a rebuild drops them.
func (s guardState) liveBridges() []string {
	var live []string
	for _, bridge := range s.bridges {
		if _, e := net.InterfaceByName(bridge); e == nil {
			live = append(live, bridge)
		}
	}
	return live
}

// readGuard reads the guard table and checks every chain and rule against what the guard
// would write. Rules are compared in their netlink encoding, which is what the kernel acts on.
func readGuard(conn *nftables.Conn) (guardState, error) {
	var state guardState
	tables, e := conn.ListTablesOfFamily(nftables.TableFamilyINet)
	if e != nil {
		return state, e
	}
	var table *nftables.Table
	for _, t := range tables {
		if t.Name == egressTable {
			table = t
		}
	}
	if table == nil {
		return state, nil
	}
	state.present = true
	chains, e := conn.ListChainsOfTableFamily(nftables.TableFamilyINet)
	if e != nil {
		return state, e
	}
	var owned []*nftables.Chain
	for _, c := range chains {
		if c.Table.Name == egressTable {
			owned = append(owned, c)
		}
	}
	// Any table flag changes how the table applies; a dormant table, for one, hooks nothing.
	state.sound = table.Flags == 0 && len(owned) == len(guardChains)
	seen := map[string]int{}
	for _, want := range guardChains {
		index := slices.IndexFunc(owned, func(c *nftables.Chain) bool { return c.Name == want.name })
		if index < 0 {
			state.sound = false
			continue
		}
		chain := owned[index]
		if !baseChainMatches(chain, want) {
			state.sound = false
		}
		rules, e := conn.GetRules(table, chain)
		if e != nil {
			return state, e
		}
		bridges, sound := ruleGroups(rules, want)
		state.sound = state.sound && sound
		for _, bridge := range bridges {
			if seen[bridge] == 0 {
				state.bridges = append(state.bridges, bridge)
			}
			seen[bridge]++
		}
	}
	for _, count := range seen {
		if count != len(guardChains) {
			state.sound = false
		}
	}
	return state, nil
}

func baseChainMatches(c *nftables.Chain, want guardChain) bool {
	return c.Type == nftables.ChainTypeFilter && c.Hooknum != nil && *c.Hooknum == *want.hook &&
		c.Priority != nil && *c.Priority == egressPriority && c.Policy != nil && *c.Policy == nftables.ChainPolicyAccept
}

// ruleGroups splits the rules of one chain into per-bridge groups by comment and reports
// whether every group is exactly what the guard writes for its bridge, in order.
func ruleGroups(rules []*nftables.Rule, want guardChain) ([]string, bool) {
	var bridges []string
	sound := true
	for start := 0; start < len(rules); {
		bridge, ok := ruleBridge(rules[start])
		if !ok || slices.Contains(bridges, bridge) {
			sound = false
			start++
			continue
		}
		bridges = append(bridges, bridge)
		expected := want.rules(bridge)
		for offset, exprs := range expected {
			index := start + offset
			if index >= len(rules) {
				sound = false
				break
			}
			if other, ok := ruleBridge(rules[index]); !ok || other != bridge || !sameExpressions(rules[index].Exprs, exprs) {
				sound = false
			}
		}
		start += len(expected)
	}
	return bridges, sound
}

func ruleBridge(rule *nftables.Rule) (string, bool) {
	comment, ok := userdata.GetString(rule.UserData, userdata.TypeComment)
	if !ok || !strings.HasPrefix(comment, egressComment) {
		return "", false
	}
	bridge := strings.TrimPrefix(comment, egressComment)
	return bridge, bridgeName.MatchString(bridge)
}

func sameExpressions(got, want []expr.Any) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		a, e := expr.Marshal(byte(nftables.TableFamilyINet), got[i])
		if e != nil {
			return false
		}
		b, e := expr.Marshal(byte(nftables.TableFamilyINet), want[i])
		if e != nil || !bytes.Equal(a, b) {
			return false
		}
	}
	return true
}

// writeGuard replaces the guard table with one that guards bridges, in a single batch.
// With no bridges left, the table is deleted.
func writeGuard(conn *nftables.Conn, present bool, bridges []string) error {
	table := &nftables.Table{Family: nftables.TableFamilyINet, Name: egressTable}
	if present {
		conn.DelTable(table)
	}
	var unique []string
	for _, bridge := range bridges {
		if !slices.Contains(unique, bridge) {
			unique = append(unique, bridge)
		}
	}
	if len(unique) > 0 {
		conn.AddTable(table)
		accept := nftables.ChainPolicyAccept
		priority := egressPriority
		for _, spec := range guardChains {
			chain := conn.AddChain(&nftables.Chain{Name: spec.name, Table: table, Type: nftables.ChainTypeFilter,
				Hooknum: spec.hook, Priority: &priority, Policy: &accept})
			for _, bridge := range unique {
				comment := userdata.AppendString(nil, userdata.TypeComment, egressComment+bridge)
				for _, exprs := range spec.rules(bridge) {
					conn.AddRule(&nftables.Rule{Table: table, Chain: chain, Exprs: exprs, UserData: comment})
				}
			}
		}
	}
	return conn.Flush()
}
