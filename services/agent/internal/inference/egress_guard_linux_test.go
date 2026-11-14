//go:build linux

package inference

import (
	"testing"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"github.com/google/nftables/userdata"
)

func guardRules(spec guardChain, bridges ...string) []*nftables.Rule {
	var rules []*nftables.Rule
	for _, bridge := range bridges {
		comment := userdata.AppendString(nil, userdata.TypeComment, egressComment+bridge)
		for _, exprs := range spec.rules(bridge) {
			rules = append(rules, &nftables.Rule{Exprs: exprs, UserData: comment})
		}
	}
	return rules
}

func TestGuardRuleGroupsAcceptOnlyWhatTheGuardWrites(t *testing.T) {
	first, second := "br-0123456789ab", "br-ba9876543210"
	for _, spec := range guardChains {
		if bridges, sound := ruleGroups(guardRules(spec, first, second), spec); !sound || len(bridges) != 2 || bridges[0] != first || bridges[1] != second {
			t.Fatalf("%s: written rules not recognized: %v %v", spec.name, bridges, sound)
		}
		if _, sound := ruleGroups(nil, spec); !sound {
			t.Fatalf("%s: empty chain reported unsound", spec.name)
		}

		truncated := guardRules(spec, first)[:1]
		if _, sound := ruleGroups(truncated, spec); sound {
			t.Fatalf("%s: incomplete group accepted", spec.name)
		}

		foreign := append([]*nftables.Rule{{Exprs: append(matchInterface(expr.MetaKeyIIFNAME, first), &expr.Verdict{Kind: expr.VerdictAccept})}}, guardRules(spec, first)...)
		if _, sound := ruleGroups(foreign, spec); sound {
			t.Fatalf("%s: rule without a guard comment accepted", spec.name)
		}

		altered := guardRules(spec, first)
		altered[len(altered)-1].Exprs = append(matchInterface(expr.MetaKeyIIFNAME, first), &expr.Verdict{Kind: expr.VerdictAccept})
		if _, sound := ruleGroups(altered, spec); sound {
			t.Fatalf("%s: changed verdict accepted", spec.name)
		}

		renamed := guardRules(spec, first)
		renamed[0].Exprs = spec.rules(second)[0]
		if _, sound := ruleGroups(renamed, spec); sound {
			t.Fatalf("%s: rule matching another interface accepted", spec.name)
		}

		duplicated := guardRules(spec, first, first)
		if _, sound := ruleGroups(duplicated, spec); sound {
			t.Fatalf("%s: duplicated group accepted", spec.name)
		}
	}
}

func TestGuardRulesRefuseEverythingButReplies(t *testing.T) {
	bridge := "br-0123456789ab"
	verdicts := func(rules [][]expr.Any) []string {
		var out []string
		for _, rule := range rules {
			switch last := rule[len(rule)-1].(type) {
			case *expr.Reject:
				if last.Code != 3 {
					t.Fatalf("reject without administratively prohibited: %+v", last)
				}
				out = append(out, "reject")
			case *expr.Verdict:
				out = append(out, map[expr.VerdictKind]string{expr.VerdictAccept: "accept", expr.VerdictDrop: "drop"}[last.Kind])
			}
		}
		return out
	}
	if got := verdicts(forwardRules(bridge)); len(got) != 2 || got[0] != "reject" || got[1] != "drop" {
		t.Fatal("forward rules", got)
	}
	input := inputRules(bridge)
	if got := verdicts(input); len(got) != 2 || got[0] != "accept" || got[1] != "reject" {
		t.Fatal("input rules", got)
	}
	if _, ok := input[0][2].(*expr.Ct); !ok {
		t.Fatal("input accept is not limited to replies")
	}
}

func TestGuardBaseChainsRunBeforeDocker(t *testing.T) {
	accept, drop := nftables.ChainPolicyAccept, nftables.ChainPolicyDrop
	priority, late := egressPriority, nftables.ChainPriority(0)
	for _, spec := range guardChains {
		chain := &nftables.Chain{Name: spec.name, Type: nftables.ChainTypeFilter, Hooknum: spec.hook, Priority: &priority, Policy: &accept}
		if !baseChainMatches(chain, spec) {
			t.Fatalf("%s: written chain not recognized", spec.name)
		}
		if moved := *chain; true {
			moved.Priority = &late
			if baseChainMatches(&moved, spec) {
				t.Fatalf("%s: chain behind Docker's accepted", spec.name)
			}
		}
		if closed := *chain; true {
			closed.Policy = &drop
			if baseChainMatches(&closed, spec) {
				t.Fatalf("%s: changed policy accepted", spec.name)
			}
		}
	}
	if egressPriority >= 0 {
		t.Fatal("guard does not run ahead of Docker's filter chains")
	}
}

func TestGuardRefusesInvalidOrAbsentBridges(t *testing.T) {
	guard := newEgressGuard()
	for _, name := range []string{"", "eth0", "br-XYZ", "br-0123456789abc", "docker0"} {
		if e := guard.Ensure(name); e == nil || e.Error() != "INFERENCE_CONFIGURATION_INVALID" {
			t.Fatalf("%q: %v", name, e)
		}
	}
	if e := guard.Ensure("br-000000000000"); e == nil || e.Error() != egressGuardUnavailable {
		t.Fatal("guard written for a bridge outside this network namespace", e)
	}
}
