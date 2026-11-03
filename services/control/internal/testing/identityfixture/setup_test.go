package identityfixture

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestIdentitySetupDefaultsDoNotExtendLegacyBudgets(t *testing.T) {
	p, e := (SetupOptions{}).plan()
	if e != nil || p.explicit || p.statementMilliseconds != "5000" {
		t.Fatal("legacy statement policy changed", e)
	}
	ctx, cancel := p.context()
	defer cancel()
	if _, bounded := ctx.Deadline(); bounded {
		t.Fatal("legacy fixture acquired a new setup deadline")
	}
}

func TestIdentitySetupUsesIndependentConfiguration(t *testing.T) {
	runtime, e := pgxpool.ParseConfig("postgres://synthetic@127.0.0.1/synthetic_test?sslmode=disable")
	if e != nil {
		t.Fatal(e)
	}
	runtime.MaxConns = 8
	runtime.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
	runtime.ConnConfig.RuntimeParams["lock_timeout"] = "3000"
	runtime.ConnConfig.RuntimeParams["application_name"] = "original-runtime"
	p, e := (SetupOptions{TimeoutSeconds: 180, StatementTimeoutSeconds: 30}).plan()
	if e != nil {
		t.Fatal(e)
	}
	setup := p.poolConfiguration(runtime, "m01_synthetic")
	if setup.ConnConfig.RuntimeParams["statement_timeout"] != "30000" || setup.ConnConfig.RuntimeParams["lock_timeout"] != "3000" || setup.ConnConfig.RuntimeParams["search_path"] != "m01_synthetic" || setup.MaxConns != 8 {
		t.Fatal("setup did not retain its exact independent budget/identity")
	}
	if runtime.ConnConfig.RuntimeParams["statement_timeout"] != "5000" || runtime.ConnConfig.RuntimeParams["application_name"] != "original-runtime" || runtime.ConnConfig.RuntimeParams["search_path"] != "" {
		t.Fatal("setup configuration mutated the original runtime")
	}
}

func TestIdentitySetupExplicitBoundsAndRelations(t *testing.T) {
	for _, options := range []SetupOptions{{30, 5}, {180, 30}, {900, 300}} {
		p, e := options.plan()
		if e != nil || !p.explicit {
			t.Fatal("bounded explicit setup refused", options, e)
		}
		ctx, cancel := p.context()
		_, bounded := ctx.Deadline()
		cancel()
		if !bounded {
			t.Fatal("explicit setup has no total deadline")
		}
	}
	for _, options := range []SetupOptions{{0, 30}, {180, 0}, {-1, 30}, {29, 5}, {901, 30}, {180, 4}, {900, 301}, {33, 30}, {32, 30}, {180, -1}, {int(^uint(0) >> 1), 30}} {
		p, e := options.plan()
		if e == nil || e.Error() != "IDENTITY_FIXTURE_SETUP_OPTIONS_INVALID" || p != (setupPlan{}) {
			t.Fatal("invalid setup leaked a plan", options, e)
		}
	}
}
